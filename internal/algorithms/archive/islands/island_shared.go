//go:build ignore

package islands

import "conflict-graph-benchmarks/internal/model"

// ---------------------------------------------------------------------------
// Hyper-parameters shared by the island family. Each algorithm's own knobs
// (pairwise cut-off, sequential threshold) live in its own file;
// tune_hyperparams.py rewrites these literals when re-tuning.
// ---------------------------------------------------------------------------

// ObjectHubThreshold is the per-object access count above which an object is
// treated as a hub and bypassed by the Union-Find partition.
const ObjectHubThreshold uint32 = 1000000000 //default: 50

// HubToucherChunk is the number of hub touchers a worker claims at once in the
// parallel hub phase.
const HubToucherChunk = 64

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

// planIslandWork lists the work units of the parallel per-island phase: every
// island whole, except sweep islands (larger than pairwiseMax) with more than
// splitRows rows, which are cut into row ranges of splitRows. Once the index
// is built their rows are independent, so one giant island no longer bounds
// the phase. Pairwise islands stay whole: they write both rows of each pair.
func planIslandWork(topo *model.BlockTopology, pairwiseMax int, splitRows uint32) {
	units := topo.WorkUnits[:0]
	for islandIdx, island := range topo.Islands {
		k := uint32(len(island))
		step := k
		if len(island) > pairwiseMax && k > splitRows {
			step = splitRows
		}
		for lo := uint32(0); lo < k; lo += step {
			units = append(units, model.IslandSpan{Island: uint32(islandIdx), Lo: lo, Hi: min(lo+step, k)})
		}
	}
	topo.WorkUnits = units
}

// ---------------------------------------------------------------------------
// Pairwise island helpers and statistics.
// ---------------------------------------------------------------------------

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
