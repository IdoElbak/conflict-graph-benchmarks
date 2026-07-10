package model

import "math/bits"

// ConflictGraph represents an undirected adjacency matrix using a flattened bit-slice
type ConflictGraph struct {
	// graph is a flattened bit-matrix
	// Each row of the adjacency matrix is stored sequentially using BucketSize uint64 buckets.
	graph []uint64
}

// NewConflictGraph initializes a graph with pre-allocated memory for MaximumGraphSize
func NewConflictGraph() *ConflictGraph {
	return &ConflictGraph{make([]uint64, MaximumGraphSize*BucketSize)}
}

// AddEdge sets a bit in the upper triangle to record a conflict between two transactions
func (conflictGraph *ConflictGraph) AddEdge(txnIdx, peerIdx uint32) {
	// Calculate the index of the 64-bit word in the flat slice
	wordIdx := txnIdx*BucketSize + peerIdx/SizeOfUint64

	// Find the specific bit position within that word
	bitIdx := peerIdx % SizeOfUint64

	// Mark the conflict in the upper triangle only
	conflictGraph.graph[wordIdx] |= MaskActiveBit << bitIdx
}

// IsEdge checks if a specific edge exists in the matrix
func (conflictGraph *ConflictGraph) IsEdge(txnIdx, peerIdx uint32) bool {
	wordIdx := txnIdx*BucketSize + peerIdx/SizeOfUint64
	bitIdx := peerIdx % SizeOfUint64
	return (conflictGraph.graph[wordIdx] & (MaskActiveBit << bitIdx)) != 0
}

// Reset used after each algorithm creates the graph inorder to reset all bits to 0 - no edges
// currentBlockSize is the number of Txn in the current used block - used to only reset the relevant amount of variables
func (conflictGraph *ConflictGraph) Reset(currentBlockSize uint32) {
	clear(conflictGraph.graph[0 : currentBlockSize*BucketSize])
}

// AreGraphsEqual performs a bit-for-bit comparison between two conflict graphs.
// It only checks the memory range relevant to the currentBlockSize, ignoring any stale data in the pre-allocated buffer.
func (conflictGraph *ConflictGraph) AreGraphsEqual(otherGraph *ConflictGraph, currentBlockSize uint32) bool {
	limit := currentBlockSize * BucketSize
	for i := uint32(0); i < limit; i++ {
		if conflictGraph.graph[i] != otherGraph.graph[i] {
			return false
		}
	}
	return true
}

// AddRow performs a bulk update of a transaction's conflict row using a bit-vector
func (conflictGraph *ConflictGraph) AddRow(txnIdx uint32, row []uint64) {
	rowStartIdx := txnIdx * BucketSize
	for wordIdx := uint32(0); wordIdx < BucketSize; wordIdx++ {
		conflictGraph.graph[rowStartIdx+wordIdx] |= row[wordIdx]
	}
}

// SyncSymmetry reflects all upper-triangle conflicts to the lower triangle to complete the graph
func (conflictGraph *ConflictGraph) SyncSymmetry(blockSize uint32) {
	for txnIdx := uint32(0); txnIdx < blockSize; txnIdx++ {
		mirrorTargetWordIdx := txnIdx / SizeOfUint64
		mirrorTargetBitIdx := txnIdx % SizeOfUint64

		// pass word by word in the bucket and ignore words with no conflicts recorded in them (word = 0)
		for wordIdx := (txnIdx + 1) / SizeOfUint64; wordIdx < BucketSize; wordIdx++ {
			variableOffset := txnIdx*BucketSize + wordIdx
			if conflictGraph.graph[variableOffset] == 0 {
				continue
			}

			peerBaseIdx := wordIdx * SizeOfUint64
			activeBits := conflictGraph.graph[variableOffset]
			bitIdx := uint32(bits.TrailingZeros64(activeBits))

			for bitIdx != SizeOfUint64 {
				peerIdx := peerBaseIdx + bitIdx
				if peerIdx >= blockSize {
					break
				}
				if txnIdx < peerIdx {
					// Calculate the position of mirrored edge
					mirrorWordIdx := peerIdx*BucketSize + mirrorTargetWordIdx

					// Add mirrored edge to the graph
					conflictGraph.graph[mirrorWordIdx] |= MaskActiveBit << mirrorTargetBitIdx
				}

				activeBits &= activeBits - 1
				bitIdx = uint32(bits.TrailingZeros64(activeBits))
			}
		}
	}
}

// CountEdges returns the total number of unique undirected edges in the symmetric graph
func (conflictGraph *ConflictGraph) CountEdges(blockSize uint32) uint32 {
	var totalBits uint32 = 0
	limit := blockSize * BucketSize

	for i := uint32(0); i < limit; i++ {
		totalBits += uint32(bits.OnesCount64(conflictGraph.graph[i]))
	}

	// Because SyncSymmetry mirrored the graph, every edge is counted twice (A->B and B->A).
	return totalBits / 2
}

// Export returns a copy of the active portion of the graph for analysis serialization
func (conflictGraph *ConflictGraph) Export(currentBlockSize uint32) []uint64 {
	limit := currentBlockSize * BucketSize
	out := make([]uint64, limit)
	copy(out, conflictGraph.graph[:limit])
	return out
}
