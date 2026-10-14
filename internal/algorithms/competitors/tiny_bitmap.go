// Package competitors holds the algorithms the island family is measured
// against, all writing the full conflict matrix: BitmapTiny (Bitmap, the
// baseline) and HybridJoinTiny (HybridJoin, the strongest Phase 3
// construction), each with the one-word TinyBitmap kernel for blocks of at
// most 64 transactions. One file per algorithm (bitmap_tiny.go,
// hybrid_join_tiny.go) and the shared one-word kernel (tiny_bitmap.go; the
// island algorithms use it too). SortJoin is archived. All are built to the
// same standard as the island algorithms: no allocation per call, rows sized
// to the live block, the shared worker pool with a sequential threshold, and
// the access filters.
package competitors

import (
	"math/bits"

	"conflict-graph-benchmarks/internal/model"
)

// TinyBitmapMax is the largest block TinyBitmap builds: one word per row.
const TinyBitmapMax = 64

// tinyClearPerTxn: TinyBitmap clears its index word-wide when the block has
// at most this many objects per transaction, and wipes it access by access
// otherwise (a 64-transaction sample of a block with thousands of objects).
const tinyClearPerTxn = 16

// TinyBitmap is Bitmap specialized to blocks of at most TinyBitmapMax
// transactions, where every row is one word. At that width the shared-object
// filter costs more than the ORs it saves, and it is not needed for
// correctness: a private object's bits are the transaction's own, cleared with
// the self bit, and a never-written object has no writer bits to give a
// reader. So the index is set in one pass over both access sets with no
// per-object state, the rows are swept in a second pass, and the index is
// re-zeroed word-wide instead of access by access. It is reusable scratch:
// steady-state calls allocate nothing.
type TinyBitmap struct {
	writers, readers []uint64 // per object, one word; all-zero between calls
}

// Run builds the full matrix of a block of at most TinyBitmapMax
// transactions into graph, overwriting every row.
func (k *TinyBitmap) Run(block *model.Block, graph *model.ConflictGraph) {
	n := uint32(len(block.Transactions))
	graph.SetSize(n)
	rows, _ := graph.Matrix(n)
	k.Build(block.Transactions, block.NumUniqueObjects, rows)
}

// Build writes the one-word row of txns[t] (at most TinyBitmapMax
// transactions, object IDs below numObjects) to rows[t], as conflicts among
// txns only.
func (k *TinyBitmap) Build(txns []model.Transaction, numObjects uint32, rows []uint64) {
	k.writers = model.Resize(k.writers, int(numObjects))
	k.readers = model.Resize(k.readers, int(numObjects))
	writers, readers := k.writers, k.readers
	for t := range txns {
		bit := uint64(1) << t
		for _, obj := range txns[t].WriteSet {
			writers[obj] |= bit
		}
		for _, obj := range txns[t].ReadSet {
			readers[obj] |= bit
		}
	}
	for t := range txns {
		var row uint64
		for _, obj := range txns[t].WriteSet {
			row |= writers[obj] | readers[obj]
		}
		for _, obj := range txns[t].ReadSet {
			row |= writers[obj]
		}
		rows[t] = row &^ (1 << t)
	}
	if numObjects <= tinyClearPerTxn*uint32(len(txns)) {
		clear(writers)
		clear(readers)
		return
	}
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			writers[obj] = 0
		}
		for _, obj := range txns[t].ReadSet {
			readers[obj] = 0
		}
	}
}

// Edges returns the number of edges in the first n one-word rows Build wrote.
func Edges(rows []uint64) int {
	total := 0
	for _, row := range rows {
		total += bits.OnesCount64(row)
	}
	return total / 2
}
