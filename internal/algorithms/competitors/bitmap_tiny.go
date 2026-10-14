package competitors

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// BitmapTiny is Bitmap with TinyBitmap for blocks of at most TinyBitmapMax
// transactions: the fairness counterpart of the island family's tiny-block
// path (islands.HubConfig.TinyMax).
type BitmapTiny struct {
	tiny  TinyBitmap
	index *model.ObjectInvertedIndex
}

// NewBitmapTiny returns a BitmapTiny; it is reusable scratch.
func NewBitmapTiny() *BitmapTiny {
	return &BitmapTiny{index: model.NewInvertedIndex()}
}

// Run builds the block's full matrix into graph.
func (b *BitmapTiny) Run(block *model.Block, graph *model.ConflictGraph) {
	if len(block.Transactions) <= TinyBitmapMax {
		b.tiny.Run(block, graph)
		return
	}
	BitmapConstruction(block, graph, b.index)
}

// BitmapSequentialMax is the block size at or below which Bitmap sweeps its
// rows inline, because starting workers costs more than it saves.
const BitmapSequentialMax uint32 = 518

// BitmapChunkRows is the number of rows a worker claims at once.
const BitmapChunkRows = 16

// BitmapConstruction builds the full symmetric conflict matrix: each
// transaction's row is the word-wide OR of the index rows it can conflict
// through. Only shared objects are swept (written, and touched by a second
// transaction); the others cannot produce an edge, the same filter the island
// algorithms use. index is reusable scratch, all-zero between calls. Every row
// of graph is overwritten, so graph needs no clearing beforehand.
func BitmapConstruction(block *model.Block, graph *model.ConflictGraph, index *model.ObjectInvertedIndex) {
	n := uint32(len(block.Transactions))
	index.Prepare(block.NumUniqueObjects, n)
	index.Build(block)
	graph.SetSize(n)

	if n <= BitmapSequentialMax {
		sweepBitmapRows(block, graph, index, 0, n)
	} else {
		concurrency.ParallelFor(int(n), BitmapChunkRows, func(_, lo, hi int) {
			sweepBitmapRows(block, graph, index, uint32(lo), uint32(hi))
		})
	}

	index.Wipe(block)
}

// sweepBitmapRows writes rows [lo, hi) of graph straight into the output: a
// written shared object contributes its writers and readers (W-W, W-R), a read
// one its writers (R-W); the self bit is cleared last.
func sweepBitmapRows(block *model.Block, graph *model.ConflictGraph, index *model.ObjectInvertedIndex, lo, hi uint32) {
	for txnIdx := lo; txnIdx < hi; txnIdx++ {
		row := graph.Row(txnIdx)
		clear(row)
		txn := &block.Transactions[txnIdx]

		for _, objID := range txn.WriteSet {
			if !index.Shared(objID) {
				continue
			}
			writers := index.GetObjectWriteSetBucket(objID)[:len(row)]
			readers := index.GetObjectReadSetBucket(objID)[:len(row)]
			for bIdx := range row {
				row[bIdx] |= writers[bIdx] | readers[bIdx]
			}
		}
		for _, objID := range txn.ReadSet {
			if !index.Shared(objID) {
				continue
			}
			writers := index.GetObjectWriteSetBucket(objID)[:len(row)]
			for bIdx := range row {
				row[bIdx] |= writers[bIdx]
			}
		}

		row[txnIdx/model.SizeOfUint64] &^= model.MaskActiveBit << (txnIdx % model.SizeOfUint64)
	}
}
