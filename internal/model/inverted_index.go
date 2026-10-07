package model

// ObjectInvertedIndex holds, per object, a reader bitset and a writer bitset
// over the block's transactions, each ceil(n/64) words wide, plus a state per
// object that tells whether the object can produce an edge at all. It is
// reusable scratch: Prepare grows the arenas only when a block needs more
// words than any block before, and Wipe restores the all-zero invariant after
// each use by zeroing exactly what Build set.
type ObjectInvertedIndex struct {
	readSet  []uint64
	writeSet []uint64
	state    []uint32 // 0 = never written, else first writer + 1, | sharedObject once shared
	width    uint32
}

// sharedObject marks, in an object's state, a written object that a second
// transaction touches.
const sharedObject uint32 = 1 << 31

// NewInvertedIndex returns an empty index; Prepare sizes it for a block.
func NewInvertedIndex() *ObjectInvertedIndex {
	return &ObjectInvertedIndex{}
}

// Prepare sizes the index for numObjects objects over n transactions. The
// arenas and the states are all-zero on return (fresh, or wiped by the
// previous use).
func (invertedIndex *ObjectInvertedIndex) Prepare(numObjects, n uint32) {
	invertedIndex.width = WordsPerRow(n)
	need := int(numObjects * invertedIndex.width)
	if cap(invertedIndex.readSet) < need {
		invertedIndex.readSet = make([]uint64, need)
		invertedIndex.writeSet = make([]uint64, need)
	}
	invertedIndex.readSet = invertedIndex.readSet[:need]
	invertedIndex.writeSet = invertedIndex.writeSet[:need]
	invertedIndex.state = Resize(invertedIndex.state, int(numObjects))
}

// Build sets the writer bit of every write and the reader bit of every read
// of a written object, and marks the shared objects: written ones that a
// second transaction touches. Only shared objects can produce an edge: a read
// of a never-written object conflicts with nobody, and neither does a written
// object no other transaction touches.
func (invertedIndex *ObjectInvertedIndex) Build(block *Block) {
	width, state := invertedIndex.width, invertedIndex.state
	for txnIdx := range block.Transactions {
		txn := uint32(txnIdx)
		word, mask := txn/SizeOfUint64, MaskActiveBit<<(txn%SizeOfUint64)
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			if s := state[objID]; s == 0 {
				state[objID] = txn + 1
			} else {
				state[objID] = s | sharedObject
			}
			invertedIndex.writeSet[objID*width+word] |= mask
		}
	}
	for txnIdx := range block.Transactions {
		txn := uint32(txnIdx)
		word, mask := txn/SizeOfUint64, MaskActiveBit<<(txn%SizeOfUint64)
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			if s := state[objID]; s != 0 {
				state[objID] = s | sharedObject
				invertedIndex.readSet[objID*width+word] |= mask
			}
		}
	}
}

// Shared reports whether objID can produce an edge: it is written and a
// second transaction touches it. Valid between Build and Wipe.
func (invertedIndex *ObjectInvertedIndex) Shared(objID uint32) bool {
	return invertedIndex.state[objID] >= sharedObject
}

// Wipe re-zeroes exactly the words and the states Build set, in O(accesses):
// the reader words of written objects first (the states still tell which),
// then the writer words and the states.
func (invertedIndex *ObjectInvertedIndex) Wipe(block *Block) {
	width, state := invertedIndex.width, invertedIndex.state
	for txnIdx := range block.Transactions {
		word := uint32(txnIdx) / SizeOfUint64
		for _, objID := range block.Transactions[txnIdx].ReadSet {
			if state[objID] != 0 {
				invertedIndex.readSet[objID*width+word] = 0
			}
		}
	}
	for txnIdx := range block.Transactions {
		word := uint32(txnIdx) / SizeOfUint64
		for _, objID := range block.Transactions[txnIdx].WriteSet {
			invertedIndex.writeSet[objID*width+word] = 0
			state[objID] = 0
		}
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
