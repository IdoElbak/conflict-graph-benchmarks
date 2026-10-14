package model

import (
	"math/bits"
	"slices"
)

// ConflictGraph represents an undirected adjacency matrix using a flattened bit-slice.
// Rows are sized to the live block: each spans width = ceil(n/64) words, so
// every sweep, reset and comparison touches only the block's own words.
type ConflictGraph struct {
	graph []uint64
	width uint32
}

// NewConflictGraph initializes a graph with pre-allocated memory for MaximumGraphSize
func NewConflictGraph() *ConflictGraph {
	return &ConflictGraph{graph: make([]uint64, MaximumGraphSize*BucketSize)}
}

// WordsPerRow returns ceil(n/64), the number of uint64 words that hold one
// bit per transaction of an n-transaction block.
func WordsPerRow(n uint32) uint32 {
	return (n + SizeOfUint64 - 1) / SizeOfUint64
}

// SetSize sizes the rows for an n-transaction block without clearing them. It
// is for algorithms that overwrite every row of the block.
func (conflictGraph *ConflictGraph) SetSize(n uint32) {
	conflictGraph.width = WordsPerRow(n)
}

// Reset sizes the rows for an n-transaction block and clears them (no edges).
func (conflictGraph *ConflictGraph) Reset(n uint32) {
	conflictGraph.SetSize(n)
	clear(conflictGraph.graph[:n*conflictGraph.width])
}

// Row returns the row of txnIdx as a view of exactly one row's words.
func (conflictGraph *ConflictGraph) Row(txnIdx uint32) []uint64 {
	start := txnIdx * conflictGraph.width
	end := start + conflictGraph.width
	return conflictGraph.graph[start:end:end]
}

// Matrix returns the rows of an n-transaction block as one slice, row t at
// t*width, and the row width: for algorithms that hand the matrix on as a
// whole (an island holding the whole block).
func (conflictGraph *ConflictGraph) Matrix(n uint32) ([]uint64, uint32) {
	end := n * conflictGraph.width
	return conflictGraph.graph[:end:end], conflictGraph.width
}

// AddEdge sets the bit of peerIdx in the row of txnIdx
func (conflictGraph *ConflictGraph) AddEdge(txnIdx, peerIdx uint32) {
	wordIdx := txnIdx*conflictGraph.width + peerIdx/SizeOfUint64
	conflictGraph.graph[wordIdx] |= MaskActiveBit << (peerIdx % SizeOfUint64)
}

// IsEdge checks if a specific edge exists in the matrix
func (conflictGraph *ConflictGraph) IsEdge(txnIdx, peerIdx uint32) bool {
	wordIdx := txnIdx*conflictGraph.width + peerIdx/SizeOfUint64
	return conflictGraph.graph[wordIdx]&(MaskActiveBit<<(peerIdx%SizeOfUint64)) != 0
}

// AreGraphsEqual performs a bit-for-bit comparison of the first
// currentBlockSize rows of two graphs, which must be sized for the same block.
func (conflictGraph *ConflictGraph) AreGraphsEqual(otherGraph *ConflictGraph, currentBlockSize uint32) bool {
	return slices.Equal(conflictGraph.graph[:currentBlockSize*conflictGraph.width],
		otherGraph.graph[:currentBlockSize*otherGraph.width])
}

// AddRow ORs a bit-vector of at least one row's words into the row of txnIdx
func (conflictGraph *ConflictGraph) AddRow(txnIdx uint32, row []uint64) {
	dst := conflictGraph.Row(txnIdx)
	row = row[:len(dst)]
	for wordIdx := range dst {
		dst[wordIdx] |= row[wordIdx]
	}
}

// SyncSymmetry reflects all upper-triangle conflicts to the lower triangle to complete the graph
func (conflictGraph *ConflictGraph) SyncSymmetry(blockSize uint32) {
	width := conflictGraph.width
	for txnIdx := uint32(0); txnIdx < blockSize; txnIdx++ {
		mirrorTargetWordIdx := txnIdx / SizeOfUint64
		mirrorTargetBitIdx := txnIdx % SizeOfUint64

		// pass word by word in the row and ignore words with no conflicts recorded in them (word = 0)
		for wordIdx := (txnIdx + 1) / SizeOfUint64; wordIdx < width; wordIdx++ {
			activeBits := conflictGraph.graph[txnIdx*width+wordIdx]
			peerBaseIdx := wordIdx * SizeOfUint64
			for activeBits != 0 {
				peerIdx := peerBaseIdx + uint32(bits.TrailingZeros64(activeBits))
				activeBits &= activeBits - 1
				if txnIdx < peerIdx && peerIdx < blockSize {
					conflictGraph.graph[peerIdx*width+mirrorTargetWordIdx] |= MaskActiveBit << mirrorTargetBitIdx
				}
			}
		}
	}
}

// CountEdges returns the total number of unique undirected edges in the symmetric graph
func (conflictGraph *ConflictGraph) CountEdges(blockSize uint32) uint32 {
	var totalBits uint32 = 0
	for _, word := range conflictGraph.graph[:blockSize*conflictGraph.width] {
		totalBits += uint32(bits.OnesCount64(word))
	}

	// Every edge is stored in both rows (A->B and B->A), so it is counted twice.
	return totalBits / 2
}
