package model

import (
	"sync/atomic"
)

// MaximumGraphSize is the assumed maximum number of transactions in a block of transactions
const MaximumGraphSize uint32 = 3000
const SizeOfUint64 uint32 = 64

// BucketSize is the amount of uint64 variables it takes to represent a row in the adjacency matrix
const BucketSize uint32 = uint32((MaximumGraphSize + SizeOfUint64 - 1) / SizeOfUint64)

// maskActiveBit used in the AddEdge method that creates a mask to update the graph
const maskActiveBit uint64 = 1

// ConflictGraph represents an undirected graph of transaction contentions using a space-efficient bit-matrix
type ConflictGraph struct {
	// graph is a flattened bit-matrix
	// Each row of the adjacency matrix is stored sequentially using BucketSize uint64 buckets.
	graph []uint64
}

// NewConflictGraph creates the only instance of the ConflictGraph Struct
func NewConflictGraph() *ConflictGraph {
	return &ConflictGraph{make([]uint64, MaximumGraphSize*BucketSize)}
}

// AddEdge adds both edges (firstIndex, secondIndex) and (secondIndex, firstIndex) to the graph
// firstIndex and secondIndex refer to the transaction IDs (0,...,N-1)
func (conflictGraph *ConflictGraph) AddEdge(firstIndex uint32, secondIndex uint32) {

	// Calculate the offset in the flat slice by finding the start of the row (firstIndex * BucketSize)
	// and adding the horizontal bucket index (secondIndex / 64)
	var variableOffset uint32 = firstIndex*BucketSize + secondIndex/SizeOfUint64

	// Identify the specific bit within the 64-bit bucket using the remainder of the division by 64
	bitOffset := secondIndex % SizeOfUint64

	// Perform a safe bitwise OR to flip the specific bit to 1,
	// marking a conflict without affecting other bits in the bucket
	atomic.OrUint64(&conflictGraph.graph[variableOffset], maskActiveBit<<bitOffset)

	// Repeat the process with indices swapped to maintain the undirected (symmetric) nature of the graph.
	variableOffset = secondIndex*BucketSize + firstIndex/SizeOfUint64
	bitOffset = firstIndex % SizeOfUint64
	atomic.OrUint64(&conflictGraph.graph[variableOffset], maskActiveBit<<bitOffset)
}

// Reset used after each algorithm creates the graph inorder to reset all bits to 0 - no edges
// currentBlockSize is the number of Txn in the current used block - used to only reset the relevant amount of variables
func (conflictGraph *ConflictGraph) Reset(currentBlockSize uint32) {
	clear(conflictGraph.graph[0 : currentBlockSize*BucketSize])
}

// AreGraphsEqual performs a bit-for-bit comparison between two conflict graphs.
// It only checks the memory range relevant to the currentBlockSize, ignoring any stale data in the pre-allocated buffer.
func (conflictGraph *ConflictGraph) AreGraphsEqual(otherGraph *ConflictGraph, currentBlockSize uint32) bool {
	var i uint32
	var limit = currentBlockSize * BucketSize
	for i = 0; i < limit; i++ {
		if conflictGraph.graph[i] != otherGraph.graph[i] {
			return false
		}
	}
	return true
}
