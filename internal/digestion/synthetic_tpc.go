package digestion

import (
	"fmt"
	"math"
	"math/rand/v2"
)

// TPC-C-style synthetic workload: the five TPC-C transaction types at the
// spec's minimum mix, modeled at row granularity. Warehouse and district rows
// are the hot written state, item rows are hot but read-only (like Solana's
// programs), customers and stock are large, mostly independent spaces. The
// warehouse count is the contention knob (fewer warehouses = more conflicts),
// calibrated per block to its target density.
const (
	// TPCFilePath and TPCManifestPath are the generated trace and its manifest.
	TPCFilePath     = SyntheticDataDir + "/TPC_synthetic_blocks.json"
	TPCManifestPath = SyntheticDataDir + "/TPC_synthetic_manifest.json"

	// TPCSeed is the master seed; any change yields a different but equally
	// reproducible trace.
	TPCSeed uint64 = 0x7BCC_2026_0924

	tpcDistricts        = 10
	tpcCustomers        = 3000
	tpcItems            = 100_000
	tpcMaxWarehouseLog2 = 20
	tpcRemotePayment    = 0.15 // payments for a customer of another warehouse
	tpcRemoteSupply     = 0.01 // order lines supplied by another warehouse
	tpcStockLevelItems  = 20
	nurandCustA         = 1023
	nurandCustC         = 259
	nurandItemA         = 8191
	nurandItemC         = 7911
)

// Cumulative TPC-C mix (%): NewOrder 45, Payment 43, OrderStatus/Delivery/StockLevel 4 each.
const (
	mixNewOrder    = 45
	mixPayment     = 88
	mixOrderStatus = 92
	mixDelivery    = 96
)

// Row kinds, stored in the top 4 bits of a key.
const (
	tagWarehouse uint64 = iota + 1
	tagDistrict
	tagCustomer
	tagOrder    // a customer's latest order (w, d, c)
	tagNewOrder // a district's undelivered-order queue (w, d)
	tagStock    // (w, item)
	tagItem     // item catalogue, never written
	tagHistory  // payment history insert, unique per transaction
)

// tpcParams is the generator configuration recorded in the manifest.
type tpcParams struct {
	Mix              map[string]int `json:"mix_percent"`
	Districts        int            `json:"districts_per_warehouse"`
	Customers        int            `json:"customers_per_district"`
	Items            int            `json:"items"`
	WarehouseRange   [2]int         `json:"warehouse_range"`
	RemotePayment    float64        `json:"remote_payment"`
	RemoteSupply     float64        `json:"remote_supply"`
	NURandCustomerAC [2]int         `json:"nurand_customer_A_C"`
	NURandItemAC     [2]int         `json:"nurand_item_A_C"`
}

// GenerateTPCTrace writes the TPC-C-style synthetic trace and its manifest.
// Returns os.ErrExist if the trace already exists.
func GenerateTPCTrace() error {
	return writeSyntheticTrace(syntheticTrace{
		name:         "TPC",
		tracePath:    TPCFilePath,
		manifestPath: TPCManifestPath,
		seed:         TPCSeed,
		knobName:     "warehouses",
		params: tpcParams{
			Mix:              map[string]int{"new_order": 45, "payment": 43, "order_status": 4, "delivery": 4, "stock_level": 4},
			Districts:        tpcDistricts,
			Customers:        tpcCustomers,
			Items:            tpcItems,
			WarehouseRange:   [2]int{1, 1 << tpcMaxWarehouseLog2},
			RemotePayment:    tpcRemotePayment,
			RemoteSupply:     tpcRemoteSupply,
			NURandCustomerAC: [2]int{nurandCustA, nurandCustC},
			NURandItemAC:     [2]int{nurandItemA, nurandItemC},
		},
		build:     buildTPCBlock,
		formatKey: formatTPCKey,
	})
}

// buildTPCBlock calibrates the warehouse count so the block hits its target
// density. The bisection runs on x = -log2(warehouses) so density grows with x.
func buildTPCBlock(spec blockSpec, ec *edgeCounter) ([]synTxn, float64, float64) {
	x, txns, density := calibrate(spec.TargetDensity, -tpcMaxWarehouseLog2, 0, func(x float64) []synTxn {
		return generateTPCTxns(spec, warehousesFor(x))
	}, ec)
	return txns, float64(warehousesFor(x)), density
}

// warehousesFor maps the bisection coordinate to a warehouse count >= 1.
func warehousesFor(x float64) int {
	return max(1, int(math.Round(math.Exp2(-x))))
}

// generateTPCTxns draws spec.Size transactions from the TPC-C mix. Each draw
// consumes a fixed amount of randomness regardless of the warehouse count, so
// calibration attempts share everything except the warehouse assignment.
func generateTPCTxns(spec blockSpec, warehouses int) []synTxn {
	rng := blockRNG(TPCSeed, spec.Number, 0)
	txns := make([]synTxn, spec.Size)
	for i := range txns {
		t := &txns[i]
		w := uint64(pick(rng, warehouses))
		d := uint64(pick(rng, tpcDistricts))
		switch kind := rng.IntN(100); {
		case kind < mixNewOrder:
			tpcNewOrder(t, w, d, warehouses, rng)
		case kind < mixPayment:
			tpcPayment(t, w, d, uint64(i), warehouses, rng)
		case kind < mixOrderStatus:
			c := nurand(rng, nurandCustA, tpcCustomers, nurandCustC)
			t.add(tpcKey(tagCustomer, w, d, c), false)
			t.add(tpcKey(tagOrder, w, d, c), false)
		case kind < mixDelivery:
			tpcDelivery(t, w, rng)
		default:
			tpcStockLevel(t, w, d, rng)
		}
	}
	return txns
}

// tpcNewOrder: read warehouse tax and customer, bump the district's next order
// id, enqueue the order, and for 5–15 lines read the item and update stock.
func tpcNewOrder(t *synTxn, w, d uint64, warehouses int, rng *rand.Rand) {
	c := nurand(rng, nurandCustA, tpcCustomers, nurandCustC)
	t.add(tpcKey(tagWarehouse, w, 0, 0), false)
	t.add(tpcKey(tagDistrict, w, d, 0), true)
	t.add(tpcKey(tagCustomer, w, d, c), false)
	t.add(tpcKey(tagOrder, w, d, c), true)
	t.add(tpcKey(tagNewOrder, w, d, 0), true)
	lines := 5 + rng.IntN(11)
	for range lines {
		item := nurand(rng, nurandItemA, tpcItems, nurandItemC)
		supply := w
		if remote, u := rng.Float64() < tpcRemoteSupply, rng.Float64(); remote && warehouses > 1 {
			supply = otherWarehouse(w, warehouses, u)
		}
		t.add(tpcKey(tagItem, 0, 0, item), false)
		t.add(tpcKey(tagStock, supply, 0, item), true)
	}
}

// tpcPayment: update warehouse and district year-to-date totals and the
// customer's balance (15% from another warehouse), and insert a history row.
func tpcPayment(t *synTxn, w, d, txnIdx uint64, warehouses int, rng *rand.Rand) {
	c := nurand(rng, nurandCustA, tpcCustomers, nurandCustC)
	cw, cd := w, d
	remote, u, remoteDistrict := rng.Float64() < tpcRemotePayment, rng.Float64(), uint64(pick(rng, tpcDistricts))
	if remote && warehouses > 1 {
		cw, cd = otherWarehouse(w, warehouses, u), remoteDistrict
	}
	t.add(tpcKey(tagWarehouse, w, 0, 0), true)
	t.add(tpcKey(tagDistrict, w, d, 0), true)
	t.add(tpcKey(tagCustomer, cw, cd, c), true)
	t.add(tpcKey(tagHistory, 0, 0, txnIdx), true)
}

// tpcDelivery: for every district of the warehouse, dequeue the oldest order,
// set its carrier, and credit its customer.
func tpcDelivery(t *synTxn, w uint64, rng *rand.Rand) {
	for d := range uint64(tpcDistricts) {
		c := nurand(rng, nurandCustA, tpcCustomers, nurandCustC)
		t.add(tpcKey(tagNewOrder, w, d, 0), true)
		t.add(tpcKey(tagOrder, w, d, c), true)
		t.add(tpcKey(tagCustomer, w, d, c), true)
	}
}

// tpcStockLevel: read the district's next order id and the stock of the
// items in its recent order lines.
func tpcStockLevel(t *synTxn, w, d uint64, rng *rand.Rand) {
	t.add(tpcKey(tagDistrict, w, d, 0), false)
	for range tpcStockLevelItems {
		t.add(tpcKey(tagStock, w, 0, nurand(rng, nurandItemA, tpcItems, nurandItemC)), false)
	}
}

// pick returns a uniform index in [0, n) from exactly one random draw.
func pick(rng *rand.Rand, n int) int {
	return int(rng.Float64() * float64(n))
}

// otherWarehouse maps u in [0,1) to a warehouse different from w (needs > 1).
func otherWarehouse(w uint64, warehouses int, u float64) uint64 {
	return (w + 1 + uint64(u*float64(warehouses-1))) % uint64(warehouses)
}

// nurand is TPC-C's non-uniform random NURand(A, 0, n-1) with constant c.
func nurand(rng *rand.Rand, a, n, c int) uint64 {
	return uint64(((pick(rng, a+1) | pick(rng, n)) + c) % n)
}

// tpcKey packs a row identity: tag(4) | warehouse(24) | district(4) | id(32).
func tpcKey(tag, w, d, id uint64) uint64 {
	return tag<<60 | w<<36 | d<<32 | id
}

// formatTPCKey renders a packed key as a readable object name.
func formatTPCKey(key uint64) string {
	tag, w, d, id := key>>60, key>>36&(1<<24-1), key>>32&0xF, key&(1<<32-1)
	switch tag {
	case tagWarehouse:
		return fmt.Sprintf("warehouse:%d", w)
	case tagDistrict:
		return fmt.Sprintf("district:%d:%d", w, d)
	case tagCustomer:
		return fmt.Sprintf("customer:%d:%d:%d", w, d, id)
	case tagOrder:
		return fmt.Sprintf("order:%d:%d:%d", w, d, id)
	case tagNewOrder:
		return fmt.Sprintf("neworder:%d:%d", w, d)
	case tagStock:
		return fmt.Sprintf("stock:%d:%d", w, id)
	case tagItem:
		return fmt.Sprintf("item:%d", id)
	default:
		return fmt.Sprintf("history:%d", id)
	}
}
