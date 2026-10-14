//go:build ignore

package ufnext

import (
	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/model"
)

// wholeBlock builds a block as a single island holding every transaction, for
// the blocks where a partition cannot pay: tiny blocks with competitors'
// one-word TinyBitmap, dense ones with HybridJoin. The island is exact but
// gives the downstream colouring no parallelism inside it; on such blocks the
// islands would be ~10 transactions or one giant anyway.
type wholeBlock struct {
	tiny     competitors.TinyBitmap
	tinyRows [competitors.TinyBitmapMax]uint64
	join     *competitors.HybridJoin
	graph    *model.ConflictGraph
	identity []uint32
}

// newWholeBlock returns the scratch of the whole-block paths.
func newWholeBlock() wholeBlock {
	w := wholeBlock{
		join:     competitors.NewHybridJoin(competitors.DefaultHybridJoinConfig),
		graph:    model.NewConflictGraph(),
		identity: make([]uint32, model.MaximumGraphSize),
	}
	for t := range w.identity {
		w.identity[t] = uint32(t)
	}
	return w
}

// tinyInto builds a block of at most competitors.TinyBitmapMax transactions
// into out; it returns (islands, largest, mean size).
func (w *wholeBlock) tinyInto(block *model.Block, out *model.IslandTopology) (int, int, float64) {
	n := len(block.Transactions)
	rows := w.tinyRows[:n]
	w.tiny.Build(block.Transactions, block.NumUniqueObjects, rows)
	out.SetWholeBlock(w.identity[:n], rows, 1)
	return 1, n, float64(n)
}

// joinInto builds the block with HybridJoin into out; it returns (islands,
// largest, mean size).
func (w *wholeBlock) joinInto(block *model.Block, out *model.IslandTopology) (int, int, float64) {
	w.join.Run(block, w.graph)
	return w.publish(block, out)
}

// publish makes the block's matrix out's only island.
func (w *wholeBlock) publish(block *model.Block, out *model.IslandTopology) (int, int, float64) {
	n := len(block.Transactions)
	matrix, width := w.graph.Matrix(uint32(n))
	out.SetWholeBlock(w.identity[:n], matrix, width)
	return 1, n, float64(n)
}
