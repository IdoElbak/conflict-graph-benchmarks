package algorithms

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

const TxnHubThreshold int = 100

// IslandConstructionTxnShattered isolates massive transactions from the Union-Find phase
func IslandConstructionTxnShattered(block *model.Block, graph *model.ConflictGraph) {
	n := uint32(len(block.Transactions))

	// Detect Hub Transactions
	isHubTxn := make([]bool, n)
	var hubTxnsList []uint32
	for txnIdx := uint32(0); txnIdx < n; txnIdx++ {
		txn := block.Transactions[txnIdx]
		if len(txn.ReadSet)+len(txn.WriteSet) > TxnHubThreshold {
			isHubTxn[txnIdx] = true
			hubTxnsList = append(hubTxnsList, txnIdx)
		}
	}

	// Populate Bucket without the hubs
	bucket := populateIDBucketWithoutHubs(block, isHubTxn)

	// Isolate & Group
	uf := model.NewUnionFind(n)
	partitionViaStarTopology(block.NumUniqueObjects, bucket, uf, nil)
	islands := groupValidIslands(n, uf)

	// Process Disjoint Islands
	concurrency.ParallelRun(len(islands), func(jobQueue <-chan int) {
		for islandIdx := range jobQueue {
			processSingleIsland(islands[islandIdx], block, graph)
		}
	})

	// Safely Reintegrate Hub Transactions via Row Ownership
	concurrency.ParallelRun(int(n), func(jobQueue <-chan int) {
		for txnIdx := range jobQueue {
			reintegrateTxnRow(uint32(txnIdx), hubTxnsList, block, graph)
		}
	})

	graph.SyncSymmetry(n)
}

// populateIDBucketWithoutHubs builds object access bucket while ignoring "hub transactions"
func populateIDBucketWithoutHubs(block *model.Block, isHubTxn []bool) *model.ObjectIdBucket {
	bucket := model.NewIdBucket(block.NumUniqueObjects)
	counts := countObjectAccesses(block)

	for objID := uint32(0); objID < block.NumUniqueObjects; objID++ {
		bucket.Preallocate(objID, counts[objID].reads, counts[objID].writes)
	}

	for txnIdx := uint32(0); txnIdx < uint32(len(block.Transactions)); txnIdx++ {
		if isHubTxn[txnIdx] {
			continue
		}
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			bucket.Insert(txnIdx, objID, true)
		}
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			bucket.Insert(txnIdx, objID, false)
		}
	}
	return bucket
}

// reintegrateTxnRow executes a pairwise check per transaction
func reintegrateTxnRow(txnIdx uint32, hubTxnsList []uint32, block *model.Block, graph *model.ConflictGraph) {
	for _, hubIdx := range hubTxnsList {
		// Only check the edge if current thread owns the upper-triangle row
		if txnIdx < hubIdx {
			if block.Transactions[txnIdx].HasConflict(&block.Transactions[hubIdx]) {
				graph.AddEdge(txnIdx, hubIdx)
			}
		}
	}
}
