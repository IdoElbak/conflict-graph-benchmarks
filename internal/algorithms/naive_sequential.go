package algorithms

import "conflict-graph-benchmarks/internal/model"

// NaiveSequential is the naive all-pair comparison algorithm to build an undirected conflict graph
// It passes only over the upper triangular part as there are no self-edges and AddEdge maintains symmetry
func NaiveSequential(block *model.Block, graph *model.ConflictGraph) {
	var n = len(block.Transactions)

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if block.Transactions[i].HasConflict(&block.Transactions[j]) {
				graph.AddEdge(uint32(i), uint32(j))
			}
		}
	}
}
