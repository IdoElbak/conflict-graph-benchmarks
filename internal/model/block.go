package model

// Block represents a collection of transactions processed as a single unit during an algorithm run
type Block struct {
	Transactions     []Transaction
	numUniqueObjects uint32
}
