//go:build ignore

package islands

import (
	"math/bits"

	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// IslandConstructionDynamicShattered scales to block size compared to IslandConstructionObjectShattered.
func IslandConstructionDynamicShattered(block *model.Block, graph *model.ConflictGraph) (int, int, float64) {
	n := uint32(len(block.Transactions))

	// shrink the bitwise sweep range based on block size, not max capacity
	actualBucketSize := (n + model.SizeOfUint64 - 1) / model.SizeOfUint64

	// Build Master Index
	invertedIndex, counts := buildMasterIndex(block)

	// Hub Detection & Targeted Toucher Tracking
	isHubObject := make([]bool, block.NumUniqueObjects)
	var numHubs int
	for objID := uint32(0); objID < block.NumUniqueObjects; objID++ {
		if counts[objID].reads+counts[objID].writes > ObjectHubThreshold {
			isHubObject[objID] = true
			numHubs++
		}
	}

	// Get transactions that touch hub objects
	hubTouchers := getHubTouchers(isHubObject, numHubs, block)

	// Partition directly from Bitsets
	uf := model.NewUnionFind(n)
	dynamicPartition(block.NumUniqueObjects, invertedIndex, uf, isHubObject, actualBucketSize)
	islands := groupValidIslands(n, uf)

	// Process Multi-node Islands
	concurrency.ParallelRun(len(islands), func(jobQueue <-chan int) {
		conflictVector := make([]uint64, model.BucketSize)

		for islandIdx := range jobQueue {
			dynamicProcessIsland(islands[islandIdx], invertedIndex, block, graph, conflictVector, actualBucketSize)
		}
	})

	// Reintegrate Hub Objects only for the specific transactions that touched them
	if numHubs > 0 && len(hubTouchers) > 0 {
		concurrency.ParallelRun(len(hubTouchers), func(jobQueue <-chan int) {
			conflictVector := make([]uint64, model.BucketSize)

			for idx := range jobQueue {
				txnIdx := hubTouchers[idx]
				dynamicReintegrate(txnIdx, isHubObject, invertedIndex, block, graph, conflictVector, actualBucketSize)
			}
		})
	}

	return 0, 0, 0
	//return CalculateIslandStats(islands)
}

// getHubTouchers returns list of transactions that touch hub objects
func getHubTouchers(isHubObject []bool, numHubs int, block *model.Block) []uint32 {
	n := uint32(len(block.Transactions))
	var hubTouchers []uint32

	if numHubs > 0 {
		for txnIdx := uint32(0); txnIdx < n; txnIdx++ {
			txn := &block.Transactions[txnIdx]
			touchesHub := false

			for _, objID := range txn.WriteSet {
				if isHubObject[objID] {
					touchesHub = true
					break
				}
			}
			if !touchesHub {
				for _, objID := range txn.ReadSet {
					if isHubObject[objID] {
						touchesHub = true
						break
					}
				}
			}
			if touchesHub {
				hubTouchers = append(hubTouchers, txnIdx)
			}
		}
	}

	return hubTouchers
}

// dynamicPartition runs Union-Find directly from bitsets, stopping early at actualBucketSize
func dynamicPartition(numObjects uint32, invIndex *model.ObjectInvertedIndex, uf *model.UnionFind, isHubObject []bool, actualBucketSize uint32) {
	for objID := uint32(0); objID < numObjects; objID++ {
		if isHubObject != nil && isHubObject[objID] {
			continue
		}

		writers := invIndex.GetObjectWriteSetBucket(objID)
		var anchor uint32 = model.MaximumGraphSize + 1

		for wordIdx := uint32(0); wordIdx < actualBucketSize; wordIdx++ {
			activeBits := writers[wordIdx]
			if activeBits == 0 {
				continue
			}
			baseTxnIdx := wordIdx * model.SizeOfUint64
			for activeBits != 0 {
				tIdx := baseTxnIdx + uint32(bits.TrailingZeros64(activeBits))
				if anchor > model.MaximumGraphSize {
					anchor = tIdx
				} else {
					uf.Union(anchor, tIdx)
				}
				activeBits &= activeBits - 1
			}
		}

		if anchor > model.MaximumGraphSize {
			continue
		}

		readers := invIndex.GetObjectReadSetBucket(objID)
		for wordIdx := uint32(0); wordIdx < actualBucketSize; wordIdx++ {
			activeBits := readers[wordIdx]
			if activeBits == 0 {
				continue
			}
			baseTxnIdx := wordIdx * model.SizeOfUint64
			for activeBits != 0 {
				uf.Union(anchor, baseTxnIdx+uint32(bits.TrailingZeros64(activeBits)))
				activeBits &= activeBits - 1
			}
		}
	}
}

// dynamicProcessIsland evaluates clusters using targeted loops over the active block size
func dynamicProcessIsland(island []uint32, invertedIndex *model.ObjectInvertedIndex, block *model.Block, graph *model.ConflictGraph, conflictVector []uint64, actualBucketSize uint32) {
	for _, txnIdx := range island {
		txn := &block.Transactions[txnIdx]

		// Accumulate write-set conflicts
		for _, objID := range txn.WriteSet {
			writers := invertedIndex.GetObjectWriteSetBucket(objID)
			readers := invertedIndex.GetObjectReadSetBucket(objID)
			for bIdx := uint32(0); bIdx < actualBucketSize; bIdx++ {
				conflictVector[bIdx] |= writers[bIdx] | readers[bIdx]
			}
		}

		// Accumulate read-set conflicts
		for _, objID := range txn.ReadSet {
			writers := invertedIndex.GetObjectWriteSetBucket(objID)
			for bIdx := uint32(0); bIdx < actualBucketSize; bIdx++ {
				conflictVector[bIdx] |= writers[bIdx]
			}
		}

		// Clear self-conflict bit
		wordIdx := txnIdx / model.SizeOfUint64
		bitIdx := txnIdx % model.SizeOfUint64
		conflictVector[wordIdx] &= ^(model.MaskActiveBit << bitIdx)

		graph.AddRow(txnIdx, conflictVector)

		// Fast clear
		for bIdx := uint32(0); bIdx < actualBucketSize; bIdx++ {
			conflictVector[bIdx] = 0
		}
	}
}

// dynamicReintegrate strictly processes edges for hub touchers
func dynamicReintegrate(txnIdx uint32, isHubObject []bool, invertedIndex *model.ObjectInvertedIndex, block *model.Block, graph *model.ConflictGraph, conflictVector []uint64, actualBucketSize uint32) {
	txn := &block.Transactions[txnIdx]

	// Accumulate write-set conflicts
	for _, objID := range txn.WriteSet {
		if !isHubObject[objID] {
			continue
		}
		writers := invertedIndex.GetObjectWriteSetBucket(objID)
		readers := invertedIndex.GetObjectReadSetBucket(objID)
		for bIdx := uint32(0); bIdx < actualBucketSize; bIdx++ {
			conflictVector[bIdx] |= writers[bIdx] | readers[bIdx]
		}
	}

	// Accumulate read-set conflicts
	for _, objID := range txn.ReadSet {
		if !isHubObject[objID] {
			continue
		}
		writers := invertedIndex.GetObjectWriteSetBucket(objID)
		for bIdx := uint32(0); bIdx < actualBucketSize; bIdx++ {
			conflictVector[bIdx] |= writers[bIdx]
		}
	}

	// Clear self-conflict bit
	wordIdx := txnIdx / model.SizeOfUint64
	bitIdx := txnIdx % model.SizeOfUint64
	conflictVector[wordIdx] &= ^(model.MaskActiveBit << bitIdx)

	graph.AddRow(txnIdx, conflictVector)

	// Fast clear
	for bIdx := uint32(0); bIdx < actualBucketSize; bIdx++ {
		conflictVector[bIdx] = 0
	}
}
