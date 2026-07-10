package model

// UnionFind implements an optimized, flat-array disjoint-set
type UnionFind struct {
	parent []uint32
	size   []uint32
}

// NewUnionFind creates and initializes the struct's lists
func NewUnionFind(n uint32) *UnionFind {
	parent := make([]uint32, n)
	size := make([]uint32, n)

	for i := range n {
		parent[i] = uint32(i)
		size[i] = 1
	}

	return &UnionFind{parent: parent, size: size}
}

// Find returns the root of the tree where txnIdx resides
// Uses path compression to locate the root faster in following calls
func (unionFind *UnionFind) Find(txnIdx uint32) uint32 {
	for unionFind.parent[txnIdx] != txnIdx {
		unionFind.parent[txnIdx] = unionFind.parent[unionFind.parent[txnIdx]]
		txnIdx = unionFind.parent[txnIdx]
	}
	return txnIdx
}

// Union used to unite two trees of transaction with dependencies
func (unionFind *UnionFind) Union(txnIdx1, txnIdx2 uint32) {
	root1 := unionFind.Find(txnIdx1)
	root2 := unionFind.Find(txnIdx2)

	if root1 == root2 {
		return
	}

	size1 := unionFind.size[root1]
	size2 := unionFind.size[root2]

	if size1 > size2 {
		unionFind.parent[root2] = root1
		unionFind.size[root1] += size2
	} else {
		unionFind.parent[root1] = root2
		unionFind.size[root2] += size1
	}
}

// WrapUnionFind constructs a UnionFind over externally-owned slices
func WrapUnionFind(parent, size []uint32) *UnionFind {
	return &UnionFind{parent: parent, size: size}
}

// Reset reinitializes the forest to n singleton trees in place
func (unionFind *UnionFind) Reset(n uint32) {
	for i := range n {
		unionFind.parent[i] = uint32(i)
		unionFind.size[i] = 1
	}
}

// Grow resizes the backing arrays to fit n elements, reallocating only when n exceeds current capacity
func (unionFind *UnionFind) Grow(n uint32) {
	unionFind.parent = growU32(unionFind.parent, int(n))
	unionFind.size = growU32(unionFind.size, int(n))
}
