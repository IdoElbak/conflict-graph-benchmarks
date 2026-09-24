//go:build ignore

package algorithms

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// ObjectHubThreshold now lives in island_shared.go (still used by the live
// island family); on revival this file picks it up from there automatically.

const LargeIslandThreshold = 32

// IslandConstructionObjectShattered removes highly contested objects from the Union-Find phase
func IslandConstructionObjectShattered(block *model.Block, graph *model.ConflictGraph) (int, int, float64) {
	n := uint32(len(block.Transactions))

	// Build Inverted Index & Counts simultaneously
	invertedIndex, counts := buildMasterIndex(block)

	// Scan to detect Hub Objects and count accesses
	isHubObject := make([]bool, block.NumUniqueObjects)
	var numHubs int

	for objID := uint32(0); objID < block.NumUniqueObjects; objID++ {
		if counts[objID].reads+counts[objID].writes > ObjectHubThreshold {
			isHubObject[objID] = true
			numHubs++
		}
	}

	uf := model.NewUnionFind(n)
	partitionViaInvertedIndex(block.NumUniqueObjects, invertedIndex, uf, isHubObject)
	islands := groupValidIslands(n, uf)

	// Process islands via adaptive threshold (Pairwise-naive vs Bitwise Sweep)
	concurrency.ParallelRun(len(islands), func(jobQueue <-chan int) {
		// Allocated once per worker thread
		conflictVector := make([]uint64, model.BucketSize)

		for islandIdx := range jobQueue {
			island := islands[islandIdx]
			if len(island) >= LargeIslandThreshold {
				processLargeIslandBitmap(island, invertedIndex, block, graph, conflictVector)
			} else {
				processSingleIsland(island, block, graph)
			}
		}
	})

	if numHubs > 0 {
		// Reintegrate hub objects using bitwise OR sweeps
		concurrency.ParallelRun(int(n), func(jobQueue <-chan int) {
			// Allocated once per thread
			conflictVector := make([]uint64, model.BucketSize)

			for txnIdx := range jobQueue {
				reintegrateObjectRowVectorized(uint32(txnIdx), isHubObject, invertedIndex, block, graph, conflictVector)
			}
		})
	}
	return CalculateIslandStats(islands)
}

// processLargeIslandBitmap evaluates large clusters using vector updates
func processLargeIslandBitmap(island []uint32, invertedIndex *model.ObjectInvertedIndex, block *model.Block, graph *model.ConflictGraph, conflictVector []uint64) {
	for _, txnIdx := range island {
		txn := &block.Transactions[txnIdx]

		// Accumulate write-set conflicts
		for _, objID := range txn.WriteSet {
			writers := invertedIndex.GetObjectWriteSetBucket(objID)
			readers := invertedIndex.GetObjectReadSetBucket(objID)
			for bIdx := range writers {
				conflictVector[bIdx] |= writers[bIdx] | readers[bIdx]
			}
		}

		// Accumulate read-set conflicts
		for _, objID := range txn.ReadSet {
			writers := invertedIndex.GetObjectWriteSetBucket(objID)
			for bIdx := range writers {
				conflictVector[bIdx] |= writers[bIdx]
			}
		}

		// Clear self-conflict bit
		wordIdx := txnIdx / model.SizeOfUint64
		bitIdx := txnIdx % model.SizeOfUint64
		conflictVector[wordIdx] &= ^(model.MaskActiveBit << bitIdx)

		// Bulk write row directly to the graph bit-matrix
		graph.AddRow(txnIdx, conflictVector)
		clear(conflictVector)
	}
}

// reintegrateObjectRowVectorized flushes hub object bitsets into a contiguous row write
func reintegrateObjectRowVectorized(txnIdx uint32, isHubObject []bool, invertedIndex *model.ObjectInvertedIndex, block *model.Block, graph *model.ConflictGraph, conflictVector []uint64) {
	txn := &block.Transactions[txnIdx]

	// WriteSet touches trigger conflicts against both Writers and Readers of the hub object
	for _, objID := range txn.WriteSet {
		if !isHubObject[objID] {
			continue
		}

		writers := invertedIndex.GetObjectWriteSetBucket(objID)
		readers := invertedIndex.GetObjectReadSetBucket(objID)
		for bIdx := range writers {
			conflictVector[bIdx] |= writers[bIdx] | readers[bIdx]
		}
	}

	// ReadSet touches trigger conflicts against Writers of the hub object
	for _, objID := range txn.ReadSet {
		if !isHubObject[objID] {
			continue
		}
		writers := invertedIndex.GetObjectWriteSetBucket(objID)
		for bIdx := range writers {
			conflictVector[bIdx] |= writers[bIdx]
		}
	}

	// Clear the self-conflict bit
	wordIdx := txnIdx / model.SizeOfUint64
	bitIdx := txnIdx % model.SizeOfUint64
	conflictVector[wordIdx] &= ^(model.MaskActiveBit << bitIdx)

	graph.AddRow(txnIdx, conflictVector)
	clear(conflictVector)
}
