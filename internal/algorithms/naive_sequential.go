// Package algorithms holds the parity reference, NaiveSequential, and the
// parity test that checks every algorithm against it. The algorithms
// themselves are in two families: competitors (matrix output: Bitmap,
// SortJoin, HybridJoin) and islands (the Union-Find family); retired ones are
// in archive/, build-ignored.
package algorithms

import "conflict-graph-benchmarks/internal/model"

// NaiveSequential is the naive all-pair comparison algorithm to build an undirected conflict graph.
// It is the definitional reference the benchmark verifies every other algorithm against.
// It passes only over the upper triangular part and mirrors it with a symmetry pass.
func NaiveSequential(block *model.Block, graph *model.ConflictGraph) {
	n := uint32(len(block.Transactions))
	graph.Reset(n)

	for txnIdx := uint32(0); txnIdx < n; txnIdx++ {
		for peerIdx := txnIdx + 1; peerIdx < n; peerIdx++ {
			if block.Transactions[txnIdx].HasConflict(&block.Transactions[peerIdx]) {
				graph.AddEdge(txnIdx, peerIdx)
			}
		}
	}

	// Execute symmetry pass to mirror the upper-triangular part of the graph to the lower
	graph.SyncSymmetry(n)
}
