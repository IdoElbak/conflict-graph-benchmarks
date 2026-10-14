//go:build ignore

package ufnext

import (
	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/model"
)

// IslandVariant is an island algorithm: it builds a block's topology.
type IslandVariant interface {
	Run(*model.Block) (int, int, float64)
	Topology() *model.IslandTopology
}

// AdaptiveConfig holds Adaptive's knobs.
type AdaptiveConfig struct {
	// TinyMax: blocks of at most this many transactions (<=
	// competitors.TinyBitmapMax) are built by the one-word kernel; 0 = off.
	TinyMax int
	// DenseDensity: blocks of at least DenseMinTxns transactions whose
	// sampled density reaches it are built by HybridJoin; 0 = off.
	DenseDensity float64
	DenseMinTxns int
	// SampleTxns transactions (<= 64), evenly strided over the block, are the
	// density sample.
	SampleTxns int
}

// DefaultAdaptiveConfig holds the knobs the proving ground picked.
var DefaultAdaptiveConfig = AdaptiveConfig{
	TinyMax:      64,
	DenseDensity: 0.05,
	DenseMinTxns: 256,
	SampleTxns:   64,
}

// Adaptive routes each block to the construction that pays for it, and
// otherwise runs an island algorithm (normally IslandLeanHub):
//   - tiny blocks (Sui, ~10 transactions): every row is one word, so a
//     partition can only add work; competitors.TinyBitmap builds the block;
//   - dense blocks: one island holds most of the block, so the partition is
//     overhead on top of a Bitmap-like sweep; HybridJoin, the best dense
//     construction, builds it. The density is estimated by the one-word
//     kernel on a strided sample. (Next has a cheaper detector built into the
//     partition: GiantShare.)
//
// Both paths output the whole block as a single island (wholeBlock).
type Adaptive struct {
	cfg    AdaptiveConfig
	inner  IslandVariant
	whole  wholeBlock
	out    model.IslandTopology
	last   *model.IslandTopology
	sample []model.Transaction
	rows   [64]uint64
}

// NewAdaptive returns an Adaptive around inner with the given knobs. It is
// reusable scratch: steady-state calls allocate nothing.
func NewAdaptive(cfg AdaptiveConfig, inner IslandVariant) *Adaptive {
	cfg.TinyMax = min(cfg.TinyMax, competitors.TinyBitmapMax)
	return &Adaptive{cfg: cfg, inner: inner, whole: newWholeBlock(), sample: make([]model.Transaction, 64)}
}

// Topology returns the output of the last Run.
func (a *Adaptive) Topology() *model.IslandTopology {
	return a.last
}

// Run builds the block's topology and returns (islands, largest, mean size).
func (a *Adaptive) Run(block *model.Block) (int, int, float64) {
	n := len(block.Transactions)
	switch {
	case n <= a.cfg.TinyMax:
		a.last = &a.out
		return a.whole.tinyInto(block, &a.out)
	case a.cfg.DenseDensity > 0 && n >= a.cfg.DenseMinTxns && a.sampledDensity(block) >= a.cfg.DenseDensity:
		a.last = &a.out
		return a.whole.joinInto(block, &a.out)
	}
	a.last = a.inner.Topology()
	return a.inner.Run(block)
}

// sampledDensity returns the conflict density among SampleTxns transactions
// evenly strided over the block.
func (a *Adaptive) sampledDensity(block *model.Block) float64 {
	txns := block.Transactions
	k := min(a.cfg.SampleTxns, len(txns), 64)
	if k < 2 {
		return 0
	}
	stride := len(txns) / k
	for i := range k {
		a.sample[i] = txns[i*stride]
	}
	a.whole.tiny.Build(a.sample[:k], block.NumUniqueObjects, a.rows[:k])
	return float64(competitors.Edges(a.rows[:k])) / float64(k*(k-1)/2)
}
