package algorithms

import (
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// IDBucketConstruction builds the conflict graph using sparse memory buckets
func IDBucketConstruction(block *model.Block, graph *model.ConflictGraph) {
	n := len(block.Transactions)
	bucket := populateIDBucket(block)

	concurrency.ParallelRun(n, func(jobQueue <-chan int) {
		bucketWorker(bucket, block, graph, jobQueue)
	})

	// Execute symmetry pass to mirror the upper-triangular part of the graph to the lower
	graph.SyncSymmetry(uint32(n))
}

// populateIDBucket pre-allocates slices for ObjectIdBucket and fills them with the transaction IDs
func populateIDBucket(block *model.Block) *model.ObjectIdBucket {
	bucket := model.NewIdBucket(block.NumUniqueObjects)
	n := len(block.Transactions)

	// Pre-allocates exact read and write slices capacities for each object
	preallocateBucketCapacity(bucket, block)

	for txnIdx := 0; txnIdx < n; txnIdx++ {
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			bucket.Insert(uint32(txnIdx), objID, true)
		}
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			bucket.Insert(uint32(txnIdx), objID, false)
		}
	}

	return bucket
}

// preallocateBucketCapacity reserves the exact slice capacities for every object's read and write sets
func preallocateBucketCapacity(bucket *model.ObjectIdBucket, block *model.Block) {
	counts := countObjectAccesses(block)

	for objID := uint32(0); objID < block.NumUniqueObjects; objID++ {
		bucket.Preallocate(objID, counts[objID].reads, counts[objID].writes)
	}
}

// countObjectAccesses performs a single pass over all transactions to tally the exact number of readers and writers per object
func countObjectAccesses(block *model.Block) []struct{ reads, writes uint32 } {
	counts := make([]struct{ reads, writes uint32 }, block.NumUniqueObjects)
	n := len(block.Transactions)

	for txnIdx := 0; txnIdx < n; txnIdx++ {
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			counts[objID].writes++
		}

		for _, objID := range block.Transactions[txnIdx].ReadSet {
			counts[objID].reads++
		}
	}

	return counts
}

// bucketWorker lock-free parallel graph construction using row ownerships
func bucketWorker(bucket *model.ObjectIdBucket, block *model.Block, graph *model.ConflictGraph, jobQueue <-chan int) {

	for txnIdx := range jobQueue {
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			objectWriters := bucket.GetWriteSets(objID)
			addEdges(graph, uint32(txnIdx), objectWriters)

			objectReaders := bucket.GetReaders(objID)
			addEdges(graph, uint32(txnIdx), objectReaders)
		}

		for _, objID := range block.Transactions[txnIdx].ReadSet {
			objectWriters := bucket.GetWriteSets(objID)
			addEdges(graph, uint32(txnIdx), objectWriters)
		}
	}
}

// addEdges strictly records conflicts in the upper triangular matrix to enforce co-execution safety
func addEdges(graph *model.ConflictGraph, txnIdx uint32, peerIDs []uint32) {
	for _, peerIdx := range peerIDs {
		if txnIdx < peerIdx {
			graph.AddEdge(txnIdx, peerIdx)
		}
	}
}
