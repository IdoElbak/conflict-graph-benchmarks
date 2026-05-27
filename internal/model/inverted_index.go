package model

// ObjectInvertedIndex provides bitset-based tracking of read and write access for every unique object
type ObjectInvertedIndex struct {
	// readSet and writeSet are flattened bit-matrix
	// Each row of the adjacency matrix is stored sequentially using BucketSize uint64 buckets.
	readSet  []uint64
	writeSet []uint64
}

// NewInvertedIndex creates the ObjectInvertedIndex type and returns it
func NewInvertedIndex(block *Block) *ObjectInvertedIndex {
	sizeOfBitSet := uint32(block.NumUniqueObjects * BucketSize)
	readSet := make([]uint64, sizeOfBitSet)
	writeSet := make([]uint64, sizeOfBitSet)
	return &ObjectInvertedIndex{readSet: readSet, writeSet: writeSet}
}

// Insert records a transaction's access to an object using bitwise OR operations
func (invertedIndex *ObjectInvertedIndex) Insert(txnIdx uint32, objID uint32, isWrite bool) {
	// Calculate the offset in the slice by adding the bucket index where the object's access record starts
	// to the bucket index in the object's row where the transaction is located
	wordIdx := objID*BucketSize + txnIdx/SizeOfUint64

	// Identify the specific bit within the 64-bit bucket using the remainder of the division by 64
	bitIdx := txnIdx % SizeOfUint64

	if isWrite {
		invertedIndex.writeSet[wordIdx] |= MaskActiveBit << bitIdx
	} else {
		invertedIndex.readSet[wordIdx] |= MaskActiveBit << bitIdx
	}
}

// GetObjectReadSetBucket returns a slice view of the bitset representing all readers of an object
func (invertedIndex *ObjectInvertedIndex) GetObjectReadSetBucket(objID uint32) []uint64 {
	return invertedIndex.readSet[objID*BucketSize : (objID+1)*BucketSize]
}

// GetObjectWriteSetBucket returns a slice view of the bitset representing all writers of an object
func (invertedIndex *ObjectInvertedIndex) GetObjectWriteSetBucket(objID uint32) []uint64 {
	return invertedIndex.writeSet[objID*BucketSize : (objID+1)*BucketSize]
}
