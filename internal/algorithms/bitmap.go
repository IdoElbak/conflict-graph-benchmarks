package algorithms

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// BitmapConstruction builds the conflict graph using an inverted index to perform bulk bitwise conflict detection
func BitmapConstruction(block *model.Block, graph *model.ConflictGraph) {
	invertedIndex := populateInvertedIndex(block)

	n := len(block.Transactions)

	concurrency.ParallelRun(n, func(jobQueue <-chan int) {
		updateConflictsWorker(invertedIndex, block, graph, jobQueue)
	})
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
func updateConflictsWorker(invertedIndex *model.ObjectInvertedIndex, block *model.Block, graph *model.ConflictGraph, jobQueue <-chan int) {
	conflictVector := make([]uint64, model.BucketSize)

	for txnIdx := range jobQueue {
		// Add W-R and W-W conflicts to the transaction's conflict-vector
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			objectWriters := invertedIndex.GetObjectWriteSetBucket(objID)

			for bucketIdx := range objectWriters {
				conflictVector[bucketIdx] |= objectWriters[bucketIdx]
			}

			objectReaders := invertedIndex.GetObjectReadSetBucket(objID)

			for bucketIdx := range objectReaders {
				conflictVector[bucketIdx] |= objectReaders[bucketIdx]
			}
		}

		// Add R-W conflicts to the transaction's conflict-vector
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			objectWriters := invertedIndex.GetObjectWriteSetBucket(objID)

			for bucketIdx := range objectWriters {
				conflictVector[bucketIdx] |= objectWriters[bucketIdx]
			}
		}

		// Calculate the location of the transaction itself in its conflictVector
		wordIdx := uint32(txnIdx) / model.SizeOfUint64
		bitIdx := uint32(txnIdx) % model.SizeOfUint64

		// Clear the self-conflict bit to ensure the transaction does not conflict with itself
		conflictVector[wordIdx] &= ^(model.MaskActiveBit << bitIdx)

		// update the graph with the constructed conflict-vector
		graph.AddRow(uint32(txnIdx), conflictVector)

		// clear slice before moving to next task
		clear(conflictVector)
	}
}
