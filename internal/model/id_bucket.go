package model

// ObjectIdBucket provides tracking of read and write access for every unique object
type ObjectIdBucket struct {
	readSets  [][]uint32
	writeSets [][]uint32
}

// NewIdBucket creates the ObjectIdBucket type and returns it
func NewIdBucket(numUniqueObjects uint32) *ObjectIdBucket {
	readSets := make([][]uint32, numUniqueObjects)
	writeSets := make([][]uint32, numUniqueObjects)
	return &ObjectIdBucket{readSets, writeSets}
}

func (objectIdBucket *ObjectIdBucket) Preallocate(objID uint32, readCap uint32, writeCap uint32) {
	objectIdBucket.readSets[objID] = make([]uint32, 0, readCap)
	objectIdBucket.writeSets[objID] = make([]uint32, 0, writeCap)
}

// Insert records a transaction's access to an object
func (objectIdBucket *ObjectIdBucket) Insert(txnIdx uint32, objID uint32, isWrite bool) {
	if isWrite {
		objectIdBucket.writeSets[objID] = append(objectIdBucket.writeSets[objID], txnIdx)
	} else {
		objectIdBucket.readSets[objID] = append(objectIdBucket.readSets[objID], txnIdx)
	}
}

// GetReaders returns a slice representing all readers of an object
func (objectIdBucket *ObjectIdBucket) GetReaders(objID uint32) []uint32 {
	return objectIdBucket.readSets[objID]
}

// GetWriteSets returns a slice representing all writers of an object
func (objectIdBucket *ObjectIdBucket) GetWriteSets(objID uint32) []uint32 {
	return objectIdBucket.writeSets[objID]
}
