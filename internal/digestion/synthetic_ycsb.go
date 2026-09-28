package digestion

import (
	"fmt"
	"math"
	"math/rand/v2"
)

// YCSB-style synthetic workload: core workload A (50% reads, 50% updates)
// with zipfian record popularity over a fixed record space. The zipf skew
// theta is the contention knob, calibrated per block to its target density.
const (
	// YCSBFilePath and YCSBManifestPath are the generated trace and its manifest.
	YCSBFilePath     = SyntheticDataDir + "/YCSB_synthetic_blocks.json"
	YCSBManifestPath = SyntheticDataDir + "/YCSB_synthetic_manifest.json"

	// YCSBSeed is the master seed; any change yields a different but equally
	// reproducible trace.
	YCSBSeed uint64 = 0x59C5B_2026_0924

	ycsbRecordCount    = 10_000_000
	ycsbMinOps         = 4
	ycsbMaxOps         = 12
	ycsbReadProportion = 0.5
	ycsbMaxTheta       = 4.0
)

// ycsbParams is the generator configuration recorded in the manifest.
type ycsbParams struct {
	Workload       string     `json:"workload"`
	RecordCount    int        `json:"record_count"`
	OpsPerTxn      [2]int     `json:"ops_per_txn"`
	ReadProportion float64    `json:"read_proportion"`
	ThetaRange     [2]float64 `json:"theta_range"`
}

// GenerateYCSBTrace writes the YCSB synthetic trace and its manifest.
// Returns os.ErrExist if the trace already exists.
func GenerateYCSBTrace() error {
	return writeSyntheticTrace(syntheticTrace{
		name:         "YCSB",
		tracePath:    YCSBFilePath,
		manifestPath: YCSBManifestPath,
		seed:         YCSBSeed,
		knobName:     "zipf_theta",
		params: ycsbParams{
			Workload:       "A (update heavy)",
			RecordCount:    ycsbRecordCount,
			OpsPerTxn:      [2]int{ycsbMinOps, ycsbMaxOps},
			ReadProportion: ycsbReadProportion,
			ThetaRange:     [2]float64{0, ycsbMaxTheta},
		},
		build:     buildYCSBBlock,
		formatKey: func(key uint64) string { return fmt.Sprintf("user%d", key) },
	})
}

// buildYCSBBlock calibrates theta so the block hits its target density.
func buildYCSBBlock(spec blockSpec, ec *edgeCounter) ([]synTxn, float64, float64) {
	theta, txns, density := calibrate(spec.TargetDensity, 0, ycsbMaxTheta, func(theta float64) []synTxn {
		return generateYCSBTxns(spec, theta)
	}, ec)
	return txns, theta, density
}

// generateYCSBTxns draws spec.Size transactions of 4–12 operations each.
// Operation shapes (count, read/update) come from one stream and keys from
// another, so every calibration attempt shares the same shapes.
func generateYCSBTxns(spec blockSpec, theta float64) []synTxn {
	shapes := blockRNG(YCSBSeed, spec.Number, 0)
	keys := blockRNG(YCSBSeed, spec.Number, 1)
	zipf := newZipfSampler(ycsbRecordCount, theta)

	txns := make([]synTxn, spec.Size)
	for i := range txns {
		ops := ycsbMinOps + shapes.IntN(ycsbMaxOps-ycsbMinOps+1)
		for range ops {
			isUpdate := shapes.Float64() >= ycsbReadProportion
			txns[i].add(uint64(zipf.sample(keys)), isUpdate)
		}
	}
	return txns
}

// zipfSampler draws ranks 1..n with P(k) ∝ k^-theta for any theta >= 0 in
// O(1) per draw, using rejection-inversion (Hörmann & Derflinger 1996, as in
// Apache Commons). Go's rand.Zipf needs s > 1, which excludes YCSB's default
// theta = 0.99.
type zipfSampler struct {
	n, theta float64
	hX1, hN  float64 // H(1.5) - 1 and H(n + 0.5)
	s        float64 // squeeze constant
}

// newZipfSampler precomputes the constants for n ranks and skew theta.
func newZipfSampler(n int, theta float64) zipfSampler {
	z := zipfSampler{n: float64(n), theta: theta}
	z.hX1 = z.hIntegral(1.5) - 1
	z.hN = z.hIntegral(z.n + 0.5)
	z.s = 2 - z.hIntegralInverse(z.hIntegral(2.5)-z.h(2))
	return z
}

// sample returns one rank in [1, n].
func (z zipfSampler) sample(rng *rand.Rand) int {
	for {
		u := z.hN + rng.Float64()*(z.hX1-z.hN)
		x := z.hIntegralInverse(u)
		k := min(max(math.Floor(x+0.5), 1), z.n)
		if k-x <= z.s || u >= z.hIntegral(k+0.5)-z.h(k) {
			return int(k)
		}
	}
}

// h is the unnormalized probability x^-theta.
func (z zipfSampler) h(x float64) float64 {
	return math.Exp(-z.theta * math.Log(x))
}

// hIntegral is H(x) = (x^(1-theta) - 1) / (1 - theta), or log x at theta = 1.
func (z zipfSampler) hIntegral(x float64) float64 {
	logX := math.Log(x)
	return expm1OverX((1-z.theta)*logX) * logX
}

// hIntegralInverse inverts hIntegral.
func (z zipfSampler) hIntegralInverse(x float64) float64 {
	t := max(x*(1-z.theta), -1) // clamp numerical noise below the domain edge
	return math.Exp(log1pOverX(t) * x)
}

// log1pOverX is log(1+x)/x, Taylor-expanded near 0 for precision.
func log1pOverX(x float64) float64 {
	if math.Abs(x) > 1e-8 {
		return math.Log1p(x) / x
	}
	return 1 - x*(0.5-x*(1.0/3-0.25*x))
}

// expm1OverX is (e^x - 1)/x, Taylor-expanded near 0 for precision.
func expm1OverX(x float64) float64 {
	if math.Abs(x) > 1e-8 {
		return math.Expm1(x) / x
	}
	return 1 + x*0.5*(1+x/3*(1+0.25*x))
}
