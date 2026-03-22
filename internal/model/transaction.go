package model

// Transaction stores the ID and sorted access sets (Read/Write) for a single execution unit
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

	return hasOverlap(firstTransaction.ReadSet, secondTransaction.ReadSet, firstTransaction.WriteSet, secondTransaction.WriteSet)
}

// -################################################
//    idea to use bitmasks for accessing lists.
// -################################################

// hasOverlap is a private helper using both two-pointer and three-pointer approaches to find overlaps in the accesses lists
func hasOverlap(firstReadSet, secondReadset, firstWriteSet, secondWriteSet []uint32) bool {
	firstReadSetCounter, secondReadsetCounter := 0, 0
	firstWriteSetCounter, secondWriteSetCounter := 0, 0

	// checking for overlaps between firstWriteSet and either of secondReadset or secondWriteSet
	for firstWriteSetCounter < len(firstWriteSet) {

		for secondWriteSetCounter < len(secondWriteSet) &&
			secondWriteSet[secondWriteSetCounter] < firstWriteSet[firstWriteSetCounter] {
			secondWriteSetCounter++
		}
		if secondWriteSetCounter < len(secondWriteSet) &&
			firstWriteSet[firstWriteSetCounter] == secondWriteSet[secondWriteSetCounter] {
			return true
		}

		for secondReadsetCounter < len(secondReadset) &&
			secondReadset[secondReadsetCounter] < firstWriteSet[firstWriteSetCounter] {
			secondReadsetCounter++
		}
		if secondReadsetCounter < len(secondReadset) &&
			firstWriteSet[firstWriteSetCounter] == secondReadset[secondReadsetCounter] {
			return true
		}

		firstWriteSetCounter++
	}

	// checking for overlaps between secondWriteSet and firstReadSet
	secondWriteSetCounter = 0
	for secondWriteSetCounter < len(secondWriteSet) {

		for firstReadSetCounter < len(firstReadSet) &&
			firstReadSet[firstReadSetCounter] < secondWriteSet[secondWriteSetCounter] {
			firstReadSetCounter++
		}
		if firstReadSetCounter < len(firstReadSet) &&
			firstReadSet[firstReadSetCounter] == secondWriteSet[secondWriteSetCounter] {
			return true
		}
		secondWriteSetCounter++
	}

	// no overlaps found
	return false
}
