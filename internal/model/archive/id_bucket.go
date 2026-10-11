//go:build ignore

package model

// ObjectIdBucket lists, per object, the transactions that write it and, for
// written objects, those that read it, in ascending order. Reads of
// never-written objects are left out: they conflict with nobody. Both lists
// are stored CSR-style: one arena of transaction indices grouped by object
// plus per-object offsets. It is reusable scratch: Build grows the arrays
// only when a block is larger than any before.
type ObjectIdBucket struct {
	readOffsets  []uint32 // readers of obj = readers[readOffsets[obj]:readOffsets[obj+1]]
	writeOffsets []uint32
	readers      []uint32
	writers      []uint32
}

// NewIdBucket returns an empty bucket; Build fills it for a block.
func NewIdBucket() *ObjectIdBucket {
	return &ObjectIdBucket{}
}

// Build groups the writes of the block, and the reads of written objects, by
// object with a counting sort.
func (objectIdBucket *ObjectIdBucket) Build(block *Block) {
	numObjects := int(block.NumUniqueObjects)
	txns := block.Transactions
	// Counts go to offsets[obj+2]; after the prefix sum offsets[obj+1] is the
	// start of obj's segment and doubles as its fill cursor, ending as the
	// start of obj+1's segment, which leaves offsets[obj]..offsets[obj+1].
	readOffsets := growCleared(objectIdBucket.readOffsets, numObjects+2)
	writeOffsets := growCleared(objectIdBucket.writeOffsets, numObjects+2)
	for txnIdx := range txns {
		for _, objID := range txns[txnIdx].WriteSet {
			writeOffsets[objID+2]++
		}
	}
	for txnIdx := range txns {
		for _, objID := range txns[txnIdx].ReadSet {
			if writeOffsets[objID+2] != 0 {
				readOffsets[objID+2]++
			}
		}
	}
	for i := 2; i < numObjects+2; i++ {
		readOffsets[i] += readOffsets[i-1]
		writeOffsets[i] += writeOffsets[i-1]
	}
	readers := Resize(objectIdBucket.readers, int(readOffsets[numObjects+1]))
	writers := Resize(objectIdBucket.writers, int(writeOffsets[numObjects+1]))
	// Reads first: an object's write segment is non-empty (it is written)
	// until the write fill moves its cursor to the segment's end.
	for txnIdx := range txns {
		for _, objID := range txns[txnIdx].ReadSet {
			if writeOffsets[objID+2] != writeOffsets[objID+1] {
				readers[readOffsets[objID+1]] = uint32(txnIdx)
				readOffsets[objID+1]++
			}
		}
	}
	for txnIdx := range txns {
		for _, objID := range txns[txnIdx].WriteSet {
			writers[writeOffsets[objID+1]] = uint32(txnIdx)
			writeOffsets[objID+1]++
		}
	}
	objectIdBucket.readOffsets, objectIdBucket.writeOffsets = readOffsets, writeOffsets
	objectIdBucket.readers, objectIdBucket.writers = readers, writers
}

// GetReaders returns all readers of a written object in ascending order (none
// for a never-written object).
func (objectIdBucket *ObjectIdBucket) GetReaders(objID uint32) []uint32 {
	return objectIdBucket.readers[objectIdBucket.readOffsets[objID]:objectIdBucket.readOffsets[objID+1]]
}

// GetWriteSets returns all writers of an object in ascending order
func (objectIdBucket *ObjectIdBucket) GetWriteSets(objID uint32) []uint32 {
	return objectIdBucket.writers[objectIdBucket.writeOffsets[objID]:objectIdBucket.writeOffsets[objID+1]]
}

// growCleared resizes a scratch slice to length need and zeroes it.
func growCleared(s []uint32, need int) []uint32 {
	s = Resize(s, need)
	clear(s)
	return s
}
