package algorithms

import (
	"conflict-graph-benchmarks/internal/model"
	"math/bits"
)

// ---------------------------------------------------------------------------
// Hyper-parameters of the island family. Every tunable knob lives here; the
// in-code values are the tuned best config from the 2026-07-11 grid search
// (tune_hyperparams.py rewrites these literals when re-tuning).
// ---------------------------------------------------------------------------

// PairwiseIslandMax is the island size at or below which direct pairwise set
// scans beat bitwise row sweeps, letting tiny islands dodge the index entirely.
const PairwiseIslandMax = 64 //default: 6

// SequentialBlockMax is the block size at or below which goroutine spawn
// overhead exceeds the parallel gain, so both phases run inline.
const SequentialBlockMax uint32 = 3000 //default: 384

// ObjectHubThreshold is the per-object access count above which an object is
// treated as a hub and bypassed by the Union-Find partition.
const ObjectHubThreshold uint32 = 1000000000 //default: 50

// ---------------------------------------------------------------------------
// Shared front-half of the Native/Topology pipelines (BlockTopology-based).
// ---------------------------------------------------------------------------

// countAccessesAndDetectHubs tallies per-object accesses and registers a hub
// the moment its count crosses ObjectHubThreshold.
func countAccessesAndDetectHubs(block *model.Block, topo *model.BlockTopology) {
	for txnIdx := range block.Transactions {
		txn := &block.Transactions[txnIdx]
		for _, objID := range txn.ReadSet {
			topo.Counts[objID]++
			if topo.Counts[objID] == ObjectHubThreshold+1 {
				topo.IsHub[objID] = true
				topo.HubObjs = append(topo.HubObjs, objID)
			}
		}
		for _, objID := range txn.WriteSet {
			topo.Counts[objID]++
			if topo.Counts[objID] == ObjectHubThreshold+1 {
				topo.IsHub[objID] = true
				topo.HubObjs = append(topo.HubObjs, objID)
			}
		}
	}
}

// collectHubTouchers appends every transaction accessing a hub object to
// topo.HubTouchers.
func collectHubTouchers(block *model.Block, topo *model.BlockTopology, n uint32) {
	for txnIdx := uint32(0); txnIdx < n; txnIdx++ {
		txn := &block.Transactions[txnIdx]
		touchesHub := false
		for _, objID := range txn.WriteSet {
			if topo.IsHub[objID] {
				touchesHub = true
				break
			}
		}
		if !touchesHub {
			for _, objID := range txn.ReadSet {
				if topo.IsHub[objID] {
					touchesHub = true
					break
				}
			}
		}
		if touchesHub {
			topo.HubTouchers = append(topo.HubTouchers, txnIdx)
		}
	}
}

// partitionViaAnchors runs the star-topology Union-Find in two O(accesses)
// passes over the raw access sets: writers claim/join each non-hub object's
// anchor, then readers join anchored objects. Read-only objects never union.
func partitionViaAnchors(block *model.Block, topo *model.BlockTopology) {
	for txnIdx := range block.Transactions {
		tIdx := uint32(txnIdx)
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			if topo.IsHub[objID] {
				continue
			}
			if anchor := topo.AnchorOf[objID]; anchor == 0 {
				topo.AnchorOf[objID] = tIdx + 1
			} else {
				topo.Union(anchor-1, tIdx)
			}
		}
	}
	for txnIdx := range block.Transactions {
		tIdx := uint32(txnIdx)
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			if topo.IsHub[objID] {
				continue
			}
			if anchor := topo.AnchorOf[objID]; anchor != 0 {
				topo.Union(anchor-1, tIdx)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Bucket/inverted-index era helpers. buildMasterIndex, partitionViaStarTopology
// and partitionViaInvertedIndex are only referenced by the archived variants in
// archive/ (kept live so revival is a plain file move).
// ---------------------------------------------------------------------------

// buildMasterIndex fuses access counting and bitmap construction into one pass
func buildMasterIndex(block *model.Block) (*model.ObjectInvertedIndex, []struct{ reads, writes uint32 }) {
	invIndex := model.NewInvertedIndex(block)
	counts := make([]struct{ reads, writes uint32 }, block.NumUniqueObjects)

	for txnIdx := range block.Transactions {
		tIdx := uint32(txnIdx)
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			invIndex.Insert(tIdx, objID, false)
			counts[objID].reads++
		}
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			invIndex.Insert(tIdx, objID, true)
			counts[objID].writes++
		}
	}
	return invIndex, counts
}

// partitionViaStarTopology links readers and writers
func partitionViaStarTopology(numObjects uint32, bucket *model.ObjectIdBucket, uf *model.UnionFind, isHubObject []bool) {
	for objID := uint32(0); objID < numObjects; objID++ {

		if isHubObject != nil && isHubObject[objID] {
			continue
		}

		writers := bucket.GetWriteSets(objID)
		if len(writers) == 0 {
			continue
		}

		anchor := writers[0]
		for i := 1; i < len(writers); i++ {
			uf.Union(anchor, writers[i])
		}

		readers := bucket.GetReaders(objID)
		for _, reader := range readers {
			uf.Union(anchor, reader)
		}
	}
}

// partitionViaInvertedIndex partitions islands directly from bitsets, bypassing sparse bucket allocation
func partitionViaInvertedIndex(numObjects uint32, invIndex *model.ObjectInvertedIndex, uf *model.UnionFind, isHubObject []bool) {
	for objID := uint32(0); objID < numObjects; objID++ {
		if isHubObject != nil && isHubObject[objID] {
			continue
		}

		writers := invIndex.GetObjectWriteSetBucket(objID)
		var anchor uint32 = model.MaximumGraphSize + 1

		// Find the first writer and union all writers to it
		for wordIdx := uint32(0); wordIdx < model.BucketSize; wordIdx++ {
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

		// Only pass this statement if there were no writers. Meaning no union operations to be called
		if anchor > model.MaximumGraphSize {
			continue
		}

		// Union all readers of this object to the Anchor writer
		readers := invIndex.GetObjectReadSetBucket(objID)
		for wordIdx := uint32(0); wordIdx < model.BucketSize; wordIdx++ {
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

// groupValidIslands aggregates the Union-Find trees and filters out singletons
func groupValidIslands(n uint32, uf *model.UnionFind) [][]uint32 {
	rawIslands := make([][]uint32, n)
	for txnIdx := uint32(0); txnIdx < n; txnIdx++ {
		root := uf.Find(txnIdx)
		rawIslands[root] = append(rawIslands[root], txnIdx)
	}

	var validIslands [][]uint32
	for _, island := range rawIslands {
		if len(island) > 1 {
			validIslands = append(validIslands, island)
		}
	}
	return validIslands
}

// processSingleIsland executes a sequential, pairwise conflict check within an isolated transaction island
func processSingleIsland(island []uint32, block *model.Block, graph *model.ConflictGraph) {
	islandSize := len(island)
	for i := 0; i < islandSize; i++ {
		for j := i + 1; j < islandSize; j++ {
			recordConflictIfPresent(island[i], island[j], block, graph)
		}
	}
}

// recordConflictIfPresent enforces symmetry constraints and records the edge
func recordConflictIfPresent(txnA, txnB uint32, block *model.Block, graph *model.ConflictGraph) {
	if txnA > txnB {
		txnA, txnB = txnB, txnA
	}
	if block.Transactions[txnA].HasConflict(&block.Transactions[txnB]) {
		graph.AddEdge(txnA, txnB)
		graph.AddEdge(txnB, txnA)
	}
}

// CalculateIslandStats returns (islandCount, maxIslandSize, avgIslandSize)
func CalculateIslandStats(islands [][]uint32) (int, int, float64) {
	count := len(islands)
	if count == 0 {
		return 0, 0, 0
	}
	maxSize := 0
	sumSize := 0
	for _, isl := range islands {
		sz := len(isl)
		if sz > maxSize {
			maxSize = sz
		}
		sumSize += sz
	}
	return count, maxSize, float64(sumSize) / float64(count)
}
