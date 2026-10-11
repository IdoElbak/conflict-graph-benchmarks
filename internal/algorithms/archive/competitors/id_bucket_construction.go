//go:build ignore

package competitors

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// IDBucketSequentialMax is the block size at or below which IDBucket emits its
// rows inline, because starting workers costs more than it saves.
const IDBucketSequentialMax uint32 = 217

// IDBucketChunkRows is the number of rows a worker claims at once.
const IDBucketChunkRows = 64

// IDBucketConstruction builds the conflict graph from per-object reader and
// writer lists (the "optimized hash-map": object IDs are dense, so the map is
// a CSR array). Reads of never-written objects never enter the lists, the
// same filter the island algorithms use. Each worker owns whole rows and sets
// one bit per conflicting peer, so both triangles are written directly and no
// symmetry pass is needed. bucket is reusable scratch; every row of graph is
// overwritten.
func IDBucketConstruction(block *model.Block, graph *model.ConflictGraph, bucket *model.ObjectIdBucket) {
	n := uint32(len(block.Transactions))
	bucket.Build(block)
	graph.SetSize(n)

	if n <= IDBucketSequentialMax {
		emitBucketRows(block, graph, bucket, 0, n)
		return
	}
	concurrency.ParallelFor(int(n), IDBucketChunkRows, func(_, lo, hi int) {
		emitBucketRows(block, graph, bucket, uint32(lo), uint32(hi))
	})
}

// emitBucketRows writes rows [lo, hi): a written object contributes its
// writers and readers, a read object its writers; the self bit is cleared last.
func emitBucketRows(block *model.Block, graph *model.ConflictGraph, bucket *model.ObjectIdBucket, lo, hi uint32) {
	for txnIdx := lo; txnIdx < hi; txnIdx++ {
		row := graph.Row(txnIdx)
		clear(row)
		txn := &block.Transactions[txnIdx]

		for _, objID := range txn.WriteSet {
			setPeerBits(row, bucket.GetWriteSets(objID))
			setPeerBits(row, bucket.GetReaders(objID))
		}
		for _, objID := range txn.ReadSet {
			setPeerBits(row, bucket.GetWriteSets(objID))
		}

		row[txnIdx/model.SizeOfUint64] &^= model.MaskActiveBit << (txnIdx % model.SizeOfUint64)
	}
}

// setPeerBits sets the bit of every peer in row.
func setPeerBits(row []uint64, peers []uint32) {
	for _, peerIdx := range peers {
		row[peerIdx/model.SizeOfUint64] |= model.MaskActiveBit << (peerIdx % model.SizeOfUint64)
	}
}
