package model

import "sync/atomic"

// ObjectInvertedIndex represents
type ObjectInvertedIndex struct {
	// readSet and writeSet are flattened bit-matrix
	// Each row of the adjacency matrix is stored sequentially using BucketSize uint64 buckets.
	readSet  []uint64
	writeSet []uint64
}

// NewInvertedIndex creates
func NewInvertedIndex(block *Block) *ObjectInvertedIndex {
	sizeOfBitSet := block.numUniqueObjects * BucketSize
	readSet := make([]uint64, sizeOfBitSet)
	writeSet := make([]uint64, sizeOfBitSet)
	return &ObjectInvertedIndex{readSet: readSet, writeSet: writeSet}
}

func (invertedIndex *ObjectInvertedIndex) Insert(transactionID uint32, objectID uint32, write bool) {
	// Calculate the offset in the flat slice by finding the start of the row (objectID * BucketSize)
	// and adding the horizontal bucket index (transactionID / 64)
	var variableOffset uint32 = objectID*BucketSize + transactionID/SizeOfUint64

	// Identify the specific bit within the 64-bit bucket using the remainder of the division by 64
	bitOffset := transactionID % SizeOfUint64

	if write {
		atomic.OrUint64(&invertedIndex.writeSet[variableOffset], maskActiveBit<<bitOffset)
	} else {
		atomic.OrUint64(&invertedIndex.readSet[variableOffset], maskActiveBit<<bitOffset)
	}
}

func (invertedIndex *ObjectInvertedIndex) GetObjectReadSetBucket(objectID uint32) []uint64 {
	return invertedIndex.readSet[objectID*BucketSize : (objectID+1)*BucketSize]
}

func (invertedIndex *ObjectInvertedIndex) GetObjectWriteSetBucket(objectID uint32) []uint64 {
	return invertedIndex.writeSet[objectID*BucketSize : (objectID+1)*BucketSize]
}
