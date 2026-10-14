//go:build ignore

package islands

import (
	"math/bits"

	"conflict-graph-benchmarks/internal/model"
)

// HubRule is a hub heuristic: it picks the objects that IslandLeanHub keeps
// out of the partition, whose conflicts go to the hub layer instead. Only
// written objects are worth picking: a never-written object neither unions
// nor conflicts. The proving ground (`go run . -suite=uf`) compares rules.
type HubRule interface {
	// Hubs appends the block's hub objects to hubs, each once, and returns it.
	Hubs(block *model.Block, hubs []uint32) []uint32
}

// PrefixHubs is the rule the Phase 4 proving ground picked: an object is a hub
// when at least MinWrites of the block's first Txns transactions write it. It
// looks at a prefix of the block only (a few hundred operations instead of a
// pass over it) and catches the objects that most transactions write, such as
// Ethereum's fee recipient.
type PrefixHubs struct {
	Txns, MinWrites int
	counts          []uint32 // per object, all-zero between calls
}

// DefaultHubRule returns the tuned rule: written by at least 4 of the first
// 64 transactions.
func DefaultHubRule() *PrefixHubs {
	return &PrefixHubs{Txns: 64, MinWrites: 4}
}

// Hubs counts the writes of the prefix and appends every object the moment
// its count reaches MinWrites; the counts it touched are re-zeroed after.
func (r *PrefixHubs) Hubs(block *model.Block, hubs []uint32) []uint32 {
	txns := block.Transactions[:min(len(block.Transactions), r.Txns)]
	need := uint32(max(2, r.MinWrites))
	r.counts = model.Resize(r.counts, int(block.NumUniqueObjects))
	counts := r.counts // a local slice: no reload of r.counts after each store
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			if counts[obj]++; counts[obj] == need {
				hubs = append(hubs, obj)
			}
		}
	}
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			counts[obj] = 0
		}
	}
	return hubs
}

// HubMinTxns is the block size below which IslandLeanHub looks for no hubs:
// such blocks fit a row in one or two words, so a hub layer cannot pay.
const HubMinTxns = 64

// hubRowsPerUnit is the number of hub-matrix rows per parallel unit.
const hubRowsPerUnit = 64

// hubAccess is one access of a hub object: the toucher index of the
// transaction and the hub number.
type hubAccess struct {
	toucher, hub uint32
}

// IslandLeanHub is IslandLean with hub shattering. Hub objects stay out of the
// partition, so the islands they glued fall apart, and every conflict through
// a hub goes to the hub layer of the output: a bit-matrix over just the
// transactions that touch a hub (toucher-local coordinates), built like
// Bitmap over the hub objects only. A dense hub, such as Ethereum's fee
// recipient written by every transaction, costs one bit per edge.
type IslandLeanHub struct {
	islandCore
	rule      HubRule
	hubObjs   []uint32
	toucherOf []uint32 // per transaction: toucher index + 1, 0 if none yet
	hubWrites []hubAccess
	hubReads  []hubAccess
	hubW      []uint64 // per hub: writer bitset over the touchers
	hubR      []uint64 // per hub: reader bitset over the touchers
	hubGraph  []uint64 // backing array of the output's HubGraph
	hubStride uint32   // words per hub-matrix row
	complete  bool     // some hub is written by every toucher: the matrix is complete

	// Parallel path: each toucher's hubs, grouped by toucher.
	wOff, rOff   []uint32 // toucher q's written hubs: wHubs[wOff[q]:wOff[q+1]]
	wHubs, rHubs []uint32
}

// NewIslandLeanHub returns an IslandLeanHub with the given knobs and hub rule
// (normally DefaultIslandConfig and DefaultHubRule()). It is reusable scratch:
// steady-state calls allocate nothing.
func NewIslandLeanHub(cfg IslandConfig, rule HubRule) *IslandLeanHub {
	h := &IslandLeanHub{islandCore: islandCore{cfg: cfg}, rule: rule}
	h.extraRun = h.sweepHubRows
	return h
}

// Run builds the block's topology and returns (islands, largest, mean size).
// On the parallel path the hub matrix rows are built alongside the islands.
func (h *IslandLeanHub) Run(block *model.Block) (int, int, float64) {
	h.begin(block)
	h.extraUnits = h.extraUnits[:0]
	if !h.markHubs(block) {
		partitionByAnchors(block, &h.forest, h.anchor)
		return h.buildIslands(block)
	}
	h.partitionWithHubs(block)
	count, largest, mean := h.collect(block)
	touchers := len(h.out.HubTouchers)
	parallel := h.parallel(block, h.localWords+touchers*int(model.WordsPerRow(uint32(touchers))))
	if parallel {
		h.planHubRows()
	} else {
		h.buildHubLayer()
	}
	h.build(block, parallel)
	return count, largest, mean
}

// markHubs asks the rule for the block's hubs and marks hub h as anchor
// hubBase+h; it reports whether there is any.
func (h *IslandLeanHub) markHubs(block *model.Block) bool {
	h.hubObjs = h.hubObjs[:0]
	if len(block.Transactions) >= HubMinTxns {
		// The default rule is called directly: through the interface, the
		// call measured ~2% slower on Ethereum-sized blocks.
		if prefix, ok := h.rule.(*PrefixHubs); ok {
			h.hubObjs = prefix.Hubs(block, h.hubObjs)
		} else {
			h.hubObjs = h.rule.Hubs(block, h.hubObjs)
		}
	}
	for hub, obj := range h.hubObjs {
		h.anchor[obj] = hubBase + uint32(hub)
	}
	return len(h.hubObjs) > 0
}

// partitionWithHubs is partitionByAnchors for a block with hubs: accesses of
// hub objects union nothing; they number their transaction as a hub toucher
// (in order of first hub access) and are recorded for the hub layer.
func (h *IslandLeanHub) partitionWithHubs(block *model.Block) {
	txns, f, anchor := block.Transactions, &h.forest, h.anchor
	h.hubWrites, h.hubReads = h.hubWrites[:0], h.hubReads[:0]
	h.toucherOf = model.Resize(h.toucherOf, len(txns))
	clear(h.toucherOf)
	for t := range txns {
		root := uint32(t)
		for _, obj := range txns[t].WriteSet {
			switch a := anchor[obj]; {
			case a == 0:
				anchor[obj] = root + 1
			case a >= hubBase:
				h.hubWrites = append(h.hubWrites, hubAccess{h.toucher(uint32(t)), a - hubBase})
			default:
				anchor[obj] = a | sharedBit
				root = join(f, root, a)
			}
		}
	}
	for t := range txns {
		root := noRoot
		for _, obj := range txns[t].ReadSet {
			switch a := anchor[obj]; {
			case a == 0:
			case a >= hubBase:
				h.hubReads = append(h.hubReads, hubAccess{h.toucher(uint32(t)), a - hubBase})
			default:
				anchor[obj] = a | sharedBit
				if root == noRoot {
					root = f.Find(uint32(t))
				}
				root = join(f, root, a)
			}
		}
	}
}

// toucher returns t's toucher index, numbering t on its first hub access.
func (h *IslandLeanHub) toucher(t uint32) uint32 {
	if q := h.toucherOf[t]; q != 0 {
		return q - 1
	}
	q := uint32(len(h.out.HubTouchers))
	h.out.HubTouchers = append(h.out.HubTouchers, t)
	h.toucherOf[t] = q + 1
	return q
}

// prepareHubLayer sizes the hub matrix (one row per toucher), builds the hub
// objects' writer and reader bitsets over the touchers and checks whether the
// matrix is complete.
func (h *IslandLeanHub) prepareHubLayer() {
	touchers := uint32(len(h.out.HubTouchers))
	hubs := uint32(len(h.hubObjs))
	stride := model.WordsPerRow(touchers)
	h.out.HubWidth, h.hubStride = stride, stride
	h.hubW = model.Resize(h.hubW, int(hubs*stride))
	h.hubR = model.Resize(h.hubR, int(hubs*stride))
	clear(h.hubW)
	clear(h.hubR)
	for _, acc := range h.hubWrites {
		h.hubW[acc.hub*stride+acc.toucher/64] |= 1 << (acc.toucher % 64)
	}
	for _, acc := range h.hubReads {
		h.hubR[acc.hub*stride+acc.toucher/64] |= 1 << (acc.toucher % 64)
	}
	h.hubGraph = model.Resize(h.hubGraph, int(touchers*stride))
	h.out.HubGraph = h.hubGraph
	h.complete = h.hasUniversalHub(touchers)
}

// hasUniversalHub reports whether some hub is written by every toucher: then
// every two touchers conflict and the hub matrix is complete.
func (h *IslandLeanHub) hasUniversalHub(touchers uint32) bool {
	stride := h.hubStride
	for hub := range uint32(len(h.hubObjs)) {
		writers := 0
		for _, word := range h.hubW[hub*stride : (hub+1)*stride] {
			writers += bits.OnesCount64(word)
		}
		if uint32(writers) == touchers {
			return true
		}
	}
	return false
}

// fillCompleteRows writes rows [lo, hi) of a complete hub matrix: every
// toucher's bit set except the row's own.
func (h *IslandLeanHub) fillCompleteRows(lo, hi uint32) {
	graph, stride := h.hubGraph, h.hubStride
	touchers := uint32(len(h.out.HubTouchers))
	last := ^uint64(0) >> ((64 - touchers%64) % 64)
	for q := lo; q < hi; q++ {
		row := graph[q*stride : (q+1)*stride]
		for k := range row {
			row[k] = ^uint64(0)
		}
		row[stride-1] = last
		row[q/64] &^= 1 << (q % 64)
	}
}

// buildHubLayer builds the hub matrix inline: each hub access is ORed into its
// toucher's row; a written hub contributes its writers and readers, a read
// hub its writers.
func (h *IslandLeanHub) buildHubLayer() {
	h.prepareHubLayer()
	touchers := uint32(len(h.out.HubTouchers))
	if h.complete {
		h.fillCompleteRows(0, touchers)
		return
	}
	graph, stride := h.hubGraph, h.hubStride
	clear(graph)
	for _, acc := range h.hubWrites {
		row := graph[acc.toucher*stride : (acc.toucher+1)*stride]
		writers := h.hubW[acc.hub*stride:][:len(row)]
		readers := h.hubR[acc.hub*stride:][:len(row)]
		for k := range row {
			row[k] |= writers[k] | readers[k]
		}
	}
	for _, acc := range h.hubReads {
		row := graph[acc.toucher*stride : (acc.toucher+1)*stride]
		writers := h.hubW[acc.hub*stride:][:len(row)]
		for k := range row {
			row[k] |= writers[k]
		}
	}
	for q := range touchers {
		graph[q*stride+q/64] &^= 1 << (q % 64)
	}
}

// planHubRows prepares the hub layer for the parallel path: it groups each
// toucher's hub accesses by toucher (counting sort) and hands the matrix rows
// to the island pass as extra units of hubRowsPerUnit touchers.
func (h *IslandLeanHub) planHubRows() {
	h.prepareHubLayer()
	touchers := len(h.out.HubTouchers)
	h.wOff, h.wHubs = groupByToucher(h.hubWrites, touchers, h.wOff, h.wHubs)
	h.rOff, h.rHubs = groupByToucher(h.hubReads, touchers, h.rOff, h.rHubs)
	for lo := 0; lo < touchers; lo += hubRowsPerUnit {
		hi := min(lo+hubRowsPerUnit, touchers)
		h.extraUnits = append(h.extraUnits, islandUnit{kind: unitExtra, lo: uint32(lo), hi: uint32(hi)})
	}
}

// groupByToucher counting-sorts accesses by toucher: the hubs of toucher q end
// up in hubIDs[off[q]:off[q+1]] (counts go to off[q+2]; after the prefix sum
// off[q+1] is the fill cursor of q and ends as the start of q+1).
func groupByToucher(accesses []hubAccess, touchers int, off, hubIDs []uint32) ([]uint32, []uint32) {
	off = model.Resize(off, touchers+2)
	clear(off)
	for _, acc := range accesses {
		off[acc.toucher+2]++
	}
	for q := 2; q < touchers+2; q++ {
		off[q] += off[q-1]
	}
	hubIDs = model.Resize(hubIDs, len(accesses))
	for _, acc := range accesses {
		hubIDs[off[acc.toucher+1]] = acc.hub
		off[acc.toucher+1]++
	}
	return off, hubIDs
}

// sweepHubRows writes rows [lo, hi) of the hub matrix from the grouped hub
// accesses (parallel path).
func (h *IslandLeanHub) sweepHubRows(lo, hi uint32) {
	if h.complete {
		h.fillCompleteRows(lo, hi)
		return
	}
	graph, stride := h.hubGraph, h.hubStride
	for q := lo; q < hi; q++ {
		row := graph[q*stride : (q+1)*stride]
		clear(row)
		for _, hub := range h.wHubs[h.wOff[q]:h.wOff[q+1]] {
			writers := h.hubW[hub*stride:][:len(row)]
			readers := h.hubR[hub*stride:][:len(row)]
			for k := range row {
				row[k] |= writers[k] | readers[k]
			}
		}
		for _, hub := range h.rHubs[h.rOff[q]:h.rOff[q+1]] {
			writers := h.hubW[hub*stride:][:len(row)]
			for k := range row {
				row[k] |= writers[k]
			}
		}
		row[q/64] &^= 1 << (q % 64)
	}
}
