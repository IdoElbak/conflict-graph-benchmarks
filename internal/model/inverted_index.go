package model

// ObjectInvertedIndex holds, per object, a reader bitset and a writer bitset
// over the block's transactions, each ceil(n/64) words wide. It is reusable
// scratch: Prepare grows the arenas only when a block needs more words than any
// block before, and Wipe restores the all-zero invariant after each use by
// zeroing exactly the words Build set.
type ObjectInvertedIndex struct {
	readSet  []uint64
	writeSet []uint64
	width    uint32
}

// NewInvertedIndex returns an empty index; Prepare sizes it for a block.
func NewInvertedIndex() *ObjectInvertedIndex {
	return &ObjectInvertedIndex{}
}

// Prepare sizes the index for numObjects objects over n transactions. The
// arenas are all-zero on return (fresh, or wiped by the previous use).
func (invertedIndex *ObjectInvertedIndex) Prepare(numObjects, n uint32) {
	invertedIndex.width = WordsPerRow(n)
	need := int(numObjects * invertedIndex.width)
	if cap(invertedIndex.readSet) < need {
		invertedIndex.readSet = make([]uint64, need)
		invertedIndex.writeSet = make([]uint64, need)
	}
	invertedIndex.readSet = invertedIndex.readSet[:need]
	invertedIndex.writeSet = invertedIndex.writeSet[:need]
}

// Build sets one bit per access of the block.
func (invertedIndex *ObjectInvertedIndex) Build(block *Block) {
	width := invertedIndex.width
	for txnIdx := range block.Transactions {
		word := uint32(txnIdx) / SizeOfUint64
		mask := MaskActiveBit << (uint32(txnIdx) % SizeOfUint64)
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			invertedIndex.readSet[objID*width+word] |= mask
		}
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			invertedIndex.writeSet[objID*width+word] |= mask
		}
	}
}

// Wipe re-zeroes exactly the words Build set for the block, in O(accesses).
func (invertedIndex *ObjectInvertedIndex) Wipe(block *Block) {
	width := invertedIndex.width
	for txnIdx := range block.Transactions {
		word := uint32(txnIdx) / SizeOfUint64
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			invertedIndex.readSet[objID*width+word] = 0
		}
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			invertedIndex.writeSet[objID*width+word] = 0
		}
	}
}

// Insert records a single access of txnIdx to objID.
func (invertedIndex *ObjectInvertedIndex) Insert(txnIdx uint32, objID uint32, isWrite bool) {
	wordIdx := objID*invertedIndex.width + txnIdx/SizeOfUint64
	mask := MaskActiveBit << (txnIdx % SizeOfUint64)
	if isWrite {
		invertedIndex.writeSet[wordIdx] |= mask
	} else {
		invertedIndex.readSet[wordIdx] |= mask
	}
}

// GetObjectReadSetBucket returns the bitset of all readers of an object
func (invertedIndex *ObjectInvertedIndex) GetObjectReadSetBucket(objID uint32) []uint64 {
	start := objID * invertedIndex.width
	end := start + invertedIndex.width
	return invertedIndex.readSet[start:end:end]
}

// GetObjectWriteSetBucket returns the bitset of all writers of an object
func (invertedIndex *ObjectInvertedIndex) GetObjectWriteSetBucket(objID uint32) []uint64 {
	start := objID * invertedIndex.width
	end := start + invertedIndex.width
	return invertedIndex.writeSet[start:end:end]
}
