//go:build ignore

package islands

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// IslandConstructionNaive builds the graph using pure Union-Find partitioning
func IslandConstructionNaive(block *model.Block, graph *model.ConflictGraph) (int, int, float64) {
	n := uint32(len(block.Transactions))
	bucket := populateIDBucket(block)
	uf := model.NewUnionFind(n)

	partitionViaStarTopology(block.NumUniqueObjects, bucket, uf, nil)
	islands := groupValidIslands(n, uf)

	concurrency.ParallelRun(len(islands), func(jobQueue <-chan int) {
		for islandIdx := range jobQueue {
			processSingleIsland(islands[islandIdx], block, graph)
		}
	})

	return CalculateIslandStats(islands)
}
