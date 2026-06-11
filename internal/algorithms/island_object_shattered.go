package algorithms

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

const ObjectHubThreshold uint32 = 50

// IslandConstructionObjectShattered removes highly contested objects from the Union-Find phase
func IslandConstructionObjectShattered(block *model.Block, graph *model.ConflictGraph) {
	n := uint32(len(block.Transactions))

	// Detect Hub Objects
	isHubObject := make([]bool, block.NumUniqueObjects)
	counts := countObjectAccesses(block)
	for objID := uint32(0); objID < block.NumUniqueObjects; objID++ {
		if counts[objID].reads+counts[objID].writes > ObjectHubThreshold {
			isHubObject[objID] = true
		}
	}

	// Isolate & Group
	bucket := populateIDBucket(block)
	uf := model.NewUnionFind(n)
	partitionViaStarTopology(block.NumUniqueObjects, bucket, uf, isHubObject)
	islands := groupValidIslands(n, uf)

	// Process Disjoint Islands
	concurrency.ParallelRun(len(islands), func(jobQueue <-chan int) {
		for islandIdx := range jobQueue {
			processSingleIsland(islands[islandIdx], block, graph)
		}
	})

	// Reintegrate Hub Objects
	concurrency.ParallelRun(int(n), func(jobQueue <-chan int) {
		for txnIdx := range jobQueue {
			reintegrateObjectRow(uint32(txnIdx), isHubObject, bucket, block, graph)
		}
	})

	graph.SyncSymmetry(n)
}

// reintegrateObjectRow allows a thread to securely write missing hub edges to its owned row
func reintegrateObjectRow(txnIdx uint32, isHubObject []bool, bucket *model.ObjectIdBucket, block *model.Block, graph *model.ConflictGraph) {
	for _, objID := range block.Transactions[txnIdx].WriteSet {
		if !isHubObject[objID] {
			continue
		}
		// Write-Write & Write-Read conflicts
		for _, peer := range bucket.GetWriteSets(objID) {
			if txnIdx < peer {
				graph.AddEdge(txnIdx, peer)
			}
		}
		for _, peer := range bucket.GetReaders(objID) {
			if txnIdx < peer {
				graph.AddEdge(txnIdx, peer)
			}
		}
	}

	for _, objID := range block.Transactions[txnIdx].ReadSet {
		if !isHubObject[objID] {
			continue
		}
		// Read-Write conflicts
		for _, peer := range bucket.GetWriteSets(objID) {
			if txnIdx < peer {
				graph.AddEdge(txnIdx, peer)
			}
		}
	}
}
