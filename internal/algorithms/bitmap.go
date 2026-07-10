package algorithms

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// BitmapConstruction builds the conflict graph using an inverted index to perform bulk bitwise conflict detection
func BitmapConstruction(block *model.Block, graph *model.ConflictGraph) (int, int, float64) {
	invertedIndex := populateInvertedIndex(block)
	n := len(block.Transactions)

	actualBucketSize := (uint32(n) + model.SizeOfUint64 - 1) / model.SizeOfUint64

	concurrency.ParallelRun(n, func(jobQueue <-chan int) {
		updateConflictsWorker(invertedIndex, block, graph, jobQueue, actualBucketSize)
	})

	return 0, 0, 0
}

// populateInvertedIndex constructs the mapping from objects to their respective transaction access bitsets
func populateInvertedIndex(block *model.Block) *model.ObjectInvertedIndex {
	invertedIndex := model.NewInvertedIndex(block)

	for txnIdx := range block.Transactions {
		for _, objectID := range block.Transactions[txnIdx].ReadSet {
			invertedIndex.Insert(uint32(txnIdx), objectID, false)
		}

		for _, objectID := range block.Transactions[txnIdx].WriteSet {
			invertedIndex.Insert(uint32(txnIdx), objectID, true)
		}
	}

	return invertedIndex
}

// updateConflictsWorker aggregates object-level bitsets into a full transaction conflict vector
func updateConflictsWorker(invertedIndex *model.ObjectInvertedIndex, block *model.Block, graph *model.ConflictGraph, jobQueue <-chan int, actualBucketSize uint32) {
	conflictVector := make([]uint64, model.BucketSize)

	for txnIdx := range jobQueue {
		// Add W-R and W-W conflicts to the transaction's conflict-vector
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			objectWriters := invertedIndex.GetObjectWriteSetBucket(objID)
			objectReaders := invertedIndex.GetObjectReadSetBucket(objID)

			for bIdx := uint32(0); bIdx < actualBucketSize; bIdx++ {
				conflictVector[bIdx] |= objectWriters[bIdx] | objectReaders[bIdx]
			}
		}

		// Add R-W conflicts to the transaction's conflict-vector
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			objectWriters := invertedIndex.GetObjectWriteSetBucket(objID)

			for bIdx := uint32(0); bIdx < actualBucketSize; bIdx++ {
				conflictVector[bIdx] |= objectWriters[bIdx]
			}
		}

		// Calculate the location of the transaction itself in its conflictVector
		wordIdx := uint32(txnIdx) / model.SizeOfUint64
		bitIdx := uint32(txnIdx) % model.SizeOfUint64

		// Clear the self-conflict bit
		conflictVector[wordIdx] &= ^(model.MaskActiveBit << bitIdx)

		// Update the graph
		graph.AddRow(uint32(txnIdx), conflictVector)

		// Fast clear
		for bIdx := uint32(0); bIdx < actualBucketSize; bIdx++ {
			conflictVector[bIdx] = 0
		}
	}
}
