package model

type Transaction struct {
	ReadSet  []uint32 // Sorted
	WriteSet []uint32 // Sorted
	ID       uint16
}

// HasConflict determines if two transactions have a resource contention.
// It returns true if there is a Write-After-Write, Write-After-Read, or Read-After-Write conflict.
func (firstTransaction *Transaction) HasConflict(secondTransaction *Transaction) bool {

	if len(firstTransaction.WriteSet) == 0 && len(secondTransaction.WriteSet) == 0 {
		return false
	}

	if hasOverlap(firstTransaction.WriteSet, secondTransaction.WriteSet) ||
		hasOverlap(firstTransaction.WriteSet, secondTransaction.ReadSet) ||
		hasOverlap(firstTransaction.ReadSet, secondTransaction.WriteSet) {
		return true
	}
	return false
}

func hasOverlap(set1, set2 []uint32) bool {
	firstCounter, secondCounter := 0, 0

	for firstCounter < len(set1) && secondCounter < len(set2) {
		if set1[firstCounter] == set2[secondCounter] {
			return true
		} else if set1[firstCounter] < set2[secondCounter] {
			firstCounter++
		} else {
			secondCounter++
		}
	}
	return false
}
