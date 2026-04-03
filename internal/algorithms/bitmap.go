package algorithms

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

func BitmapConstruction(block *model.Block, graph *model.ConflictGraph) {
	invertedIndex := populateInvertedIndex(block)

	var n = len(block.Transactions)

	concurrency.ParallelRun(n, func(jobQueue <-chan int) {
		updateConflictsWorker(invertedIndex, block, graph, jobQueue)
	})
}

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

func updateConflictsWorker(invertedIndex *model.ObjectInvertedIndex, block *model.Block, graph *model.ConflictGraph, jobQueue <-chan int) {
	var conflictVector = make([]uint64, model.BucketSize)

	for txnIdx := range jobQueue {
		for _, objectID := range block.Transactions[txnIdx].WriteSet {
			objectWriters := invertedIndex.GetObjectWriteSetBucket(objectID)

			for bucketIdx := range objectWriters {
				conflictVector[bucketIdx] |= objectWriters[bucketIdx]
			}

			objectReaders := invertedIndex.GetObjectReadSetBucket(objectID)

			for bucketIdx := range objectReaders {
				conflictVector[bucketIdx] |= objectReaders[bucketIdx]
			}
		}

		for _, objectID := range block.Transactions[txnIdx].ReadSet {
			objectWriters := invertedIndex.GetObjectWriteSetBucket(objectID)

			for bucketIdx := range objectWriters {
				conflictVector[bucketIdx] |= objectWriters[bucketIdx]
			}
		}

		// Calculate the location of the transaction itself in its conflictVector
		variableOffset := uint32(txnIdx) / model.SizeOfUint64
		bitOffset := uint32(txnIdx) % model.SizeOfUint64

		// Removing the self conflict
		conflictVector[variableOffset] &= ^(1 << bitOffset)

		graph.AddRow(uint32(txnIdx), conflictVector)

		clear(conflictVector)
	}
}
