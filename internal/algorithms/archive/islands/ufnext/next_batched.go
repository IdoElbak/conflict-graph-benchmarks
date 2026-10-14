//go:build ignore

package ufnext

import "conflict-graph-benchmarks/internal/model"

// The batched partition (NextConfig.Batched) splits the partition into a
// classification of every access and a union pass. The classification is
// branch-free: each access's anchor is updated with conditional moves and the
// access is stored to both a share buffer and a hub buffer, whose cursors
// advance only when the anchor names a first writer or a hub. On Ethereum and
// Solana 55-63% of the writes are first touches, so the partition's
// first-touch-or-shared branch is close to a coin flip; the union pass then
// runs over the shares only.

// anchorShare is one access of an object some earlier transaction wrote:
// transaction t and the object's anchor when t reached it.
type anchorShare struct {
	t, a uint32
}

// isShare is 1 for an anchor naming a first writer (in [1, hubBase)) and 0
// for a never-written object or a hub, without a branch.
func isShare(a uint32) int {
	written := 1 ^ (uint64(a)-1)>>63 // a != 0
	return int(written & (1 ^ uint64(a>>31)))
}

// isHub is 1 for a hub anchor (>= hubBase) and 0 otherwise.
func isHub(a uint32) int {
	return int(a >> 31)
}

// partitionBatched partitions the block in two branch-free classification
// passes (writes, then reads) and a union pass over the shares. Hub accesses
// land in hubWrites / hubReads under the transaction itself; the matrix hub
// layer numbers the touchers afterwards (numberTouchers).
func (h *Next) partitionBatched(block *model.Block) {
	txns, anchor := block.Transactions, h.anchor
	shares, hubW, hubR := h.shares[:cap(h.shares)], h.hubWrites[:cap(h.hubWrites)], h.hubReads[:cap(h.hubReads)]
	ns, nh := 0, 0
	for t := range txns {
		writes := txns[t].WriteSet
		if ns+len(writes) > len(shares) || nh+len(writes) > len(hubW) {
			shares, hubW = growShares(shares, ns, len(writes)), growHubs(hubW, nh, len(writes))
		}
		first := uint32(t) + 1
		for _, obj := range writes {
			a := anchor[obj]
			v := a | sharedBit
			if a == 0 {
				v = first
			}
			if a >= hubBase {
				v = a
			}
			anchor[obj] = v
			shares[ns] = anchorShare{uint32(t), a}
			ns += isShare(a)
			hubW[nh] = hubAccess{uint32(t), a - hubBase}
			nh += isHub(a)
		}
	}
	writeShares, nr := ns, 0
	for t := range txns {
		reads := txns[t].ReadSet
		if ns+len(reads) > len(shares) || nr+len(reads) > len(hubR) {
			shares, hubR = growShares(shares, ns, len(reads)), growHubs(hubR, nr, len(reads))
		}
		for _, obj := range reads {
			a := anchor[obj]
			share := isShare(a)
			anchor[obj] = a | uint32(share)<<30
			shares[ns] = anchorShare{uint32(t), a}
			ns += share
			hubR[nr] = hubAccess{uint32(t), a - hubBase}
			nr += isHub(a)
		}
	}
	h.shares, h.hubWrites, h.hubReads = shares[:ns], hubW[:nh], hubR[:nr]
	unionShares(&h.forest, h.shares[:writeShares], h.shares[writeShares:])
}

// unionShares unions each share's transaction with its object's first
// writer. Within the writes no earlier union involves t, so t is its own root
// at its first share; within the reads its root is looked up once.
func unionShares(f *model.IslandForest, writes, reads []anchorShare) {
	cur, root := noRoot, uint32(0)
	for _, s := range writes {
		if s.t != cur {
			cur, root = s.t, s.t
		}
		root = join(f, root, s.a)
	}
	cur = noRoot
	for _, s := range reads {
		if s.t != cur {
			cur, root = s.t, f.Find(s.t)
		}
		root = join(f, root, s.a)
	}
}

// numberTouchers turns the transactions of the matrix hub layer's accesses
// into toucher indices, numbered in the order the sequential partition meets
// them: hub writes first, then hub reads.
func (h *Next) numberTouchers(n int) {
	h.toucherOf = model.Resize(h.toucherOf, n)
	clear(h.toucherOf)
	for i := range h.hubWrites {
		h.hubWrites[i].who = h.toucher(h.hubWrites[i].who)
	}
	for i := range h.hubReads {
		h.hubReads[i].who = h.toucher(h.hubReads[i].who)
	}
}

// growShares returns shares with room for need more entries after the first
// used, keeping them.
func growShares(shares []anchorShare, used, need int) []anchorShare {
	if used+need <= len(shares) {
		return shares
	}
	grown := make([]anchorShare, 2*(used+need))
	copy(grown, shares[:used])
	return grown
}

// growHubs is growShares for hub accesses.
func growHubs(hubs []hubAccess, used, need int) []hubAccess {
	if used+need <= len(hubs) {
		return hubs
	}
	grown := make([]hubAccess, 2*(used+need))
	copy(grown, hubs[:used])
	return grown
}
