package digestion

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/bits"
	"math/rand/v2"
	"os"
	"runtime"
	"slices"
)

// SyntheticDataDir holds generated traces only, kept apart from real chain data.
const SyntheticDataDir = "internal/digestion/syn_data"

// DensityLevels are the target conflict ratios, defined as in the Parallel-SCT
// paper: conflict edges / all transaction pairs (= the harness' density).
// Log-spaced below 10% to cover the sparse regime of the real traces (Solana
// 0.2–12%), then the paper's 10–80% sweep.
var DensityLevels = []float64{0.001, 0.003, 0.01, 0.03, 0.1, 0.2, 0.4, 0.6, 0.8}

// sizeBand is a block-size range [Lo, Hi) and the number of blocks it gets per
// density level.
type sizeBand struct {
	Lo       int `json:"lo"`
	Hi       int `json:"hi"`
	PerLevel int `json:"per_level"`
}

// SizeBands weights generation toward the real traces' blind spots (0–100,
// 200–1000, 1200–2000); ~150 (Ethereum) and ~1180 (Solana) keep a token
// presence. 500–1000 is the densest band (since 2026-10-13, for tuning the
// dense fallback's block-size threshold). The 1200–2000 band stays sparse
// because it dominates run time.
var SizeBands = []sizeBand{
	{Lo: 0, Hi: 100, PerLevel: 5},
	{Lo: 100, Hi: 200, PerLevel: 1},
	{Lo: 200, Hi: 500, PerLevel: 4},
	{Lo: 500, Hi: 1000, PerLevel: 16},
	{Lo: 1000, Hi: 1200, PerLevel: 1},
	{Lo: 1200, Hi: 2001, PerLevel: 3},
}

// SyntheticBlockCount is the number of blocks in each synthetic trace.
var SyntheticBlockCount = countPlannedBlocks()

const (
	// calibrationSteps bounds the knob bisection; each step regenerates the block.
	calibrationSteps = 20

	// calibrationTolerance is the relative density error accepted as on-target.
	calibrationTolerance = 0.05

	// planStream is the PCG stream reserved for block planning (block streams
	// use block<<8|stream, which never reaches it).
	planStream = math.MaxUint64
)

// blockSpec describes one synthetic block before generation.
type blockSpec struct {
	Number        int64
	Size          int
	TargetDensity float64
}

// synTxn is a generated transaction over integer keys; reads and writes are
// disjoint and duplicate-free.
type synTxn struct {
	reads, writes []uint64
}

// add records one operation. A key both read and written counts as a write only.
func (t *synTxn) add(key uint64, isWrite bool) {
	if slices.Contains(t.writes, key) {
		return
	}
	if i := slices.Index(t.reads, key); i >= 0 {
		if !isWrite {
			return
		}
		t.reads = slices.Delete(t.reads, i, i+1)
	}
	if isWrite {
		t.writes = append(t.writes, key)
	} else {
		t.reads = append(t.reads, key)
	}
}

// blockBuilder generates one calibrated block and reports the knob value used
// and the realized density.
type blockBuilder func(spec blockSpec, ec *edgeCounter) (txns []synTxn, knob float64, density float64)

// syntheticTrace describes one generator for writeSyntheticTrace.
type syntheticTrace struct {
	name         string
	tracePath    string
	manifestPath string
	seed         uint64
	knobName     string
	params       any
	build        blockBuilder
	formatKey    func(key uint64) string
}

// syntheticManifest is written next to each trace so any block can be
// regenerated and verified exactly.
type syntheticManifest struct {
	Generator     string          `json:"generator"`
	Seed          uint64          `json:"seed"`
	GoVersion     string          `json:"go_version"`
	Knob          string          `json:"knob"`
	DensityLevels []float64       `json:"density_levels"`
	SizeBands     []sizeBand      `json:"size_bands"`
	Params        any             `json:"params"`
	TraceSHA256   string          `json:"trace_sha256"`
	Blocks        []manifestBlock `json:"blocks"`
}

// manifestBlock records the per-block generation outcome.
type manifestBlock struct {
	BlockNumber     int64   `json:"block_number"`
	NumTxns         int     `json:"num_txns"`
	TargetDensity   float64 `json:"target_density"`
	RealizedDensity float64 `json:"realized_density"`
	KnobValue       float64 `json:"knob_value"`
}

// countPlannedBlocks sums the blocks every size band contributes.
func countPlannedBlocks() int {
	total := 0
	for _, band := range SizeBands {
		total += band.PerLevel * len(DensityLevels)
	}
	return total
}

// blockRNG returns the deterministic random stream `stream` of one block.
func blockRNG(seed uint64, block int64, stream uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, uint64(block)<<8|stream))
}

// planBlocks lays out every synthetic block. Sizes are stratified inside each
// band; every run of len(DensityLevels) consecutive strata gets a random
// permutation of the levels, so size and contention stay balanced. The first
// block is pinned to 0 txns and the last to the band maximum (2000). The final
// order is shuffled so block size is uncorrelated with position in the run
// (thermal drift cannot masquerade as a size effect).
func planBlocks(seed uint64) []blockSpec {
	rng := rand.New(rand.NewPCG(seed, planStream))
	levels := len(DensityLevels)
	specs := make([]blockSpec, 0, SyntheticBlockCount)
	var perm []int
	for _, band := range SizeBands {
		strata := band.PerLevel * levels
		width := float64(band.Hi-band.Lo) / float64(strata)
		for s := range strata {
			if s%levels == 0 {
				perm = rng.Perm(levels)
			}
			specs = append(specs, blockSpec{
				Size:          band.Lo + int((float64(s)+rng.Float64())*width),
				TargetDensity: DensityLevels[perm[s%levels]],
			})
		}
	}
	specs[0].Size = SizeBands[0].Lo
	specs[len(specs)-1].Size = SizeBands[len(SizeBands)-1].Hi - 1

	rng.Shuffle(len(specs), func(i, j int) { specs[i], specs[j] = specs[j], specs[i] })
	for i := range specs {
		specs[i].Number = int64(i)
	}
	return specs
}

// calibrate bisects knob x in [lo, hi] and returns the generated block whose
// density is closest (relative error) to target. gen must be deterministic in
// x and produce blocks whose density grows with x.
func calibrate(target, lo, hi float64, gen func(x float64) []synTxn, ec *edgeCounter) (float64, []synTxn, float64) {
	bestX, bestErr, bestDensity := lo, math.Inf(1), 0.0
	var best []synTxn
	for range calibrationSteps {
		x := (lo + hi) / 2
		txns := gen(x)
		if len(txns) < 2 {
			return x, txns, 0 // density is undefined; nothing to calibrate
		}
		density := ec.density(txns)
		if err := math.Abs(density/target - 1); err < bestErr {
			bestX, bestErr, best, bestDensity = x, err, txns, density
		}
		if bestErr <= calibrationTolerance {
			break
		}
		if density < target {
			lo = x
		} else {
			hi = x
		}
	}
	return bestX, best, bestDensity
}

// edgeCounter counts exact conflict edges (W-W or R-W, never R-R) with per-key
// reader/writer bitsets. It is deliberately independent of the algorithms
// under test, so calibration never relies on the code being benchmarked.
type edgeCounter struct {
	keyIdx  map[uint64]int
	readers []uint64
	writers []uint64
	vec     []uint64
}

// newEdgeCounter returns an empty, reusable counter.
func newEdgeCounter() *edgeCounter {
	return &edgeCounter{keyIdx: make(map[uint64]int)}
}

// density returns edges / (n·(n-1)/2) for the block (0 when n < 2).
func (ec *edgeCounter) density(txns []synTxn) float64 {
	n := len(txns)
	if n < 2 {
		return 0
	}
	width := (n + 63) / 64
	ec.indexKeys(txns, width)

	edges := 0
	for i, t := range txns {
		clear(ec.vec)
		for _, key := range t.writes {
			base := ec.keyIdx[key] * width
			for w := range width {
				ec.vec[w] |= ec.writers[base+w] | ec.readers[base+w]
			}
		}
		for _, key := range t.reads {
			base := ec.keyIdx[key] * width
			for w := range width {
				ec.vec[w] |= ec.writers[base+w]
			}
		}
		ec.vec[i/64] &^= 1 << (i % 64)
		for _, word := range ec.vec {
			edges += bits.OnesCount64(word)
		}
	}
	return float64(edges/2) / float64(n*(n-1)/2)
}

// indexKeys assigns dense key indices and fills the reader/writer bitsets.
func (ec *edgeCounter) indexKeys(txns []synTxn, width int) {
	clear(ec.keyIdx)
	for _, t := range txns {
		for _, key := range t.reads {
			ec.intern(key)
		}
		for _, key := range t.writes {
			ec.intern(key)
		}
	}
	need := len(ec.keyIdx) * width
	ec.readers = growZeroed(ec.readers, need)
	ec.writers = growZeroed(ec.writers, need)
	ec.vec = growZeroed(ec.vec, width)

	for i, t := range txns {
		word, bit := i/64, uint64(1)<<(i%64)
		for _, key := range t.reads {
			ec.readers[ec.keyIdx[key]*width+word] |= bit
		}
		for _, key := range t.writes {
			ec.writers[ec.keyIdx[key]*width+word] |= bit
		}
	}
}

// intern gives key the next dense index if it has none yet.
func (ec *edgeCounter) intern(key uint64) {
	if _, ok := ec.keyIdx[key]; !ok {
		ec.keyIdx[key] = len(ec.keyIdx)
	}
}

// growZeroed resizes s to length need with every word zeroed.
func growZeroed(s []uint64, need int) []uint64 {
	if cap(s) < need {
		return make([]uint64, need)
	}
	s = s[:need]
	clear(s)
	return s
}

// writeSyntheticTrace generates every planned block of one trace, streams it
// in the cleaned-block JSON schema, and writes the manifest (seed, params,
// per-block outcome, SHA-256 of the trace). Returns os.ErrExist if the trace
// already exists.
func writeSyntheticTrace(st syntheticTrace) error {
	if _, err := os.Stat(st.tracePath); err == nil {
		return os.ErrExist
	}
	if err := os.MkdirAll(SyntheticDataDir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", SyntheticDataDir, err)
	}
	file, err := os.Create(st.tracePath)
	if err != nil {
		return fmt.Errorf("failed to create %s trace: %w", st.name, err)
	}
	defer file.Close()

	hash := sha256.New()
	buffered := bufio.NewWriterSize(file, 1<<20)
	manifest, err := streamBlocks(io.MultiWriter(buffered, hash), st)
	if err != nil {
		return fmt.Errorf("failed to generate %s trace: %w", st.name, err)
	}
	if err := buffered.Flush(); err != nil {
		return fmt.Errorf("failed to flush %s trace: %w", st.name, err)
	}
	manifest.TraceSHA256 = hex.EncodeToString(hash.Sum(nil))
	return writeManifest(st.manifestPath, manifest)
}

// streamBlocks builds and encodes every planned block, returning the manifest
// without its hash.
func streamBlocks(out io.Writer, st syntheticTrace) (syntheticManifest, error) {
	manifest := syntheticManifest{
		Generator:     st.name,
		Seed:          st.seed,
		GoVersion:     runtime.Version(),
		Knob:          st.knobName,
		DensityLevels: DensityLevels,
		SizeBands:     SizeBands,
		Params:        st.params,
	}
	encoder := json.NewEncoder(out)
	ec := newEdgeCounter()

	if _, err := io.WriteString(out, "[\n"); err != nil {
		return manifest, err
	}
	for i, spec := range planBlocks(st.seed) {
		txns, knob, density := st.build(spec, ec)
		if i > 0 {
			if _, err := io.WriteString(out, ",\n"); err != nil {
				return manifest, err
			}
		}
		if err := encoder.Encode(toCleanedBlock(st, spec, txns)); err != nil {
			return manifest, fmt.Errorf("failed to encode block %d: %w", spec.Number, err)
		}
		manifest.Blocks = append(manifest.Blocks, manifestBlock{
			BlockNumber:     spec.Number,
			NumTxns:         spec.Size,
			TargetDensity:   spec.TargetDensity,
			RealizedDensity: density,
			KnobValue:       knob,
		})
	}
	_, err := io.WriteString(out, "]\n")
	return manifest, err
}

// toCleanedBlock converts integer-keyed transactions to the shared schema.
func toCleanedBlock(st syntheticTrace, spec blockSpec, txns []synTxn) CleanedBlock {
	block := CleanedBlock{
		BlockNumber:     spec.Number,
		NumTransactions: len(txns),
		Transactions:    make([]CleanedTransaction, len(txns)),
	}
	for i, t := range txns {
		block.Transactions[i] = CleanedTransaction{
			ID:       fmt.Sprintf("%s-%d-%d", st.name, spec.Number, i),
			ReadSet:  formatKeys(t.reads, st.formatKey),
			WriteSet: formatKeys(t.writes, st.formatKey),
		}
	}
	return block
}

// formatKeys renders keys as object strings (never nil, so JSON shows []).
func formatKeys(keys []uint64, format func(uint64) string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = format(key)
	}
	return out
}

// writeManifest stores the manifest as indented JSON.
func writeManifest(path string, manifest syntheticManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write manifest %s: %w", path, err)
	}
	return nil
}
