package algorithms

import "conflict-graph-benchmarks/internal/model"

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

// recordConflictIfPresent enforces upper-triangular constraints and records the edge
func recordConflictIfPresent(txnA, txnB uint32, block *model.Block, graph *model.ConflictGraph) {
	if txnA > txnB {
		txnA, txnB = txnB, txnA
	}
	if block.Transactions[txnA].HasConflict(&block.Transactions[txnB]) {
		graph.AddEdge(txnA, txnB)
	}
}
