package model

// MaximumGraphSize is the assumed maximum number of transactions in a block of transactions
const MaximumGraphSize uint32 = 3000
const SizeOfUint64 uint32 = 64

// BucketSize is the amount of uint64 variables it takes to represent a row in the adjacency matrix
const BucketSize uint32 = uint32((MaximumGraphSize + SizeOfUint64 - 1) / SizeOfUint64)

// MaskActiveBit used in the AddEdge method that creates a mask to update the graph
const MaskActiveBit uint64 = 1
