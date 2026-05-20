package algorithms

import "conflict-graph-benchmarks/internal/model"

// NaiveSequential is the naive all-pair comparison algorithm to build an undirected conflict graph
// It passes only over the upper triangular part as there are no self-edges and AddEdge maintains symmetry
func NaiveSequential(block *model.Block, graph *model.ConflictGraph) {
	n := len(block.Transactions)

	for txnIdx := 0; txnIdx < n; txnIdx++ {
		for peerIdx := txnIdx + 1; peerIdx < n; peerIdx++ {
			if block.Transactions[txnIdx].HasConflict(&block.Transactions[peerIdx]) {
				graph.AddEdge(uint32(txnIdx), uint32(peerIdx))
			}
		}
	}

	// Execute symmetry pass to mirror the upper-triangular part of the graph to the lower
	graph.SyncSymmetry(uint32(n))
}
