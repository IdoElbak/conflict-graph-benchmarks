//go:build ignore

package competitors

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// NaiveParallelSequentialMax is the block size at or below which NaiveParallel
// scans inline, because starting workers costs more than it saves.
const NaiveParallelSequentialMax uint32 = 59

// NaiveParallelChunkRows is the number of rows a worker claims at once. Row i
// costs n-1-i pair checks, so small chunks keep the workers balanced.
const NaiveParallelChunkRows = 8

// NaiveParallel implements the all-pairs conflict check on a worker pool (the
// Parallel-SCT paper's Algorithm 5). A worker owns the rows it claims and
// checks each row's transaction against every later one, so the upper
// triangle is written lock-free; a symmetry pass then mirrors it.
func NaiveParallel(block *model.Block, graph *model.ConflictGraph) {
	n := uint32(len(block.Transactions))
	graph.Reset(n)

	if n <= NaiveParallelSequentialMax {
		checkPairRows(block, graph, 0, n)
	} else {
		concurrency.ParallelFor(int(n), NaiveParallelChunkRows, func(_, lo, hi int) {
			checkPairRows(block, graph, uint32(lo), uint32(hi))
		})
	}

	graph.SyncSymmetry(n)
}

// checkPairRows checks every transaction in [lo, hi) against all later ones.
func checkPairRows(block *model.Block, graph *model.ConflictGraph, lo, hi uint32) {
	n := uint32(len(block.Transactions))
	for txnIdx := lo; txnIdx < hi; txnIdx++ {
		for peerIdx := txnIdx + 1; peerIdx < n; peerIdx++ {
			if block.Transactions[txnIdx].HasConflict(&block.Transactions[peerIdx]) {
				graph.AddEdge(txnIdx, peerIdx)
			}
		}
	}
}
