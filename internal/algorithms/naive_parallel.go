package algorithms

import "conflict-graph-benchmarks/internal/model"

// NaiveParallel is the naive all-pair comparison parallel algorithm to build an undirected conflict graph
// It passes only over the upper triangular part as there are no self-edges and AddEdge maintains symmetry
func NaiveParallel(block *model.Block, graph *model.ConflictGraph) {
	var n = len(block.Transactions)
	jobs := make(chan int, n)

	for index := range jobs {
		jobs <- index
	}
}

func TxnConflictsRoutine(block *model.Block, graph *model.ConflictGraph, txnIndex uint32) {
	n := len(block.Transactions)
	for i := txnIndex + 1; i < uint32(n); i++ {
		if block.Transactions[txnIndex].HasConflict(&block.Transactions[i]) {
			graph.AddEdge(txnIndex, i)
		}
	}
}
