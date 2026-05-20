package algorithms

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// NaiveParallel implements an all-pairs conflict detection using a worker pool.
// It leverages a central job channel to distribute rows of the upper triangular
func NaiveParallel(block *model.Block, graph *model.ConflictGraph) {
	n := len(block.Transactions)

	concurrency.ParallelRun(n, func(jobQueue <-chan int) {
		conflictWorker(block, graph, jobQueue)
	})

	// Execute symmetry pass to mirror the upper-triangular part of the graph to the lower
	graph.SyncSymmetry(uint32(n))
}

// conflictWorker is a long-running goroutine that drains the jobQueue
// For every index 'i' received, it performs a conflict check against all 'j' > i
func conflictWorker(block *model.Block, graph *model.ConflictGraph, jobQueue <-chan int) {
	n := len(block.Transactions)
	for txnIdx := range jobQueue {
		for peerIdx := txnIdx + 1; peerIdx < n; peerIdx++ {
			if block.Transactions[txnIdx].HasConflict(&block.Transactions[peerIdx]) {
				graph.AddEdge(uint32(txnIdx), uint32(peerIdx))
			}
		}
	}
}
