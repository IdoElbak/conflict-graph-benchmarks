//go:build ignore

package ufnext

import "conflict-graph-benchmarks/internal/model"

// The prefix-giant dense detector (NextConfig.DenseGiant). The hub-writer
// share (DenseHubShare) asks whether the prefix writes hubs, which a single
// universal hub answers yes on every block: Ethereum's fee recipient sent
// every block of 256+ transactions to the fallback, where the islands win.
// What decides whether islands pay is the connectivity left once the hubs
// are out. So the partition runs the prefix first: its writes, then its reads
// of objects written so far (unions only). If the prefix's largest set holds
// at least DenseGiant of it, the block is dense and goes to the fallback;
// otherwise the partition continues where it stopped: the remaining writes,
// then every read (the prefix's reads again, which re-finds the same roots,
// and now records their hub accesses and their unions with later writers).

// partitionPrefixed partitions the block as partitionHubs (or
// partitionBipartite) does, prefix first; it reports whether the prefix's
// largest set reached giant transactions, in which case the partition is left
// unfinished.
func (h *Next) partitionPrefixed(block *model.Block, prefix int, giant uint32) (dense bool) {
	txns, f := block.Transactions, &h.forest
	h.hubWrites, h.hubReads = h.hubWrites[:0], h.hubReads[:0]
	if !h.ncfg.Bipartite {
		h.toucherOf = model.Resize(h.toucherOf, len(txns))
		clear(h.toucherOf)
	}
	h.writePass(txns, 0, prefix)
	h.prefixReads(txns[:prefix])
	largest := uint32(0)
	for t := range uint32(prefix) {
		if f.Find(t) == t {
			largest = max(largest, f.Size(t))
		}
	}
	if largest >= giant {
		return true
	}
	h.writePass(txns, prefix, len(txns))
	h.readPass(txns)
	return false
}

// who returns how the hub layer records transaction t: itself (bipartite) or
// its toucher index (matrix).
func (h *Next) who(t uint32) uint32 {
	if h.ncfg.Bipartite {
		return t
	}
	return h.toucher(t)
}

// writePass unions transactions [lo, hi) with the first writer of each object
// they write, recording hub writes.
func (h *Next) writePass(txns []model.Transaction, lo, hi int) {
	f, anchor := &h.forest, h.anchor
	for t := lo; t < hi; t++ {
		root := uint32(t)
		for _, obj := range txns[t].WriteSet {
			switch a := anchor[obj]; {
			case a == 0:
				anchor[obj] = root + 1
			case a >= hubBase:
				h.hubWrites = append(h.hubWrites, hubAccess{h.who(uint32(t)), a - hubBase})
			default:
				anchor[obj] = a | sharedBit
				root = join(f, root, a)
			}
		}
	}
}

// prefixReads unions the prefix's transactions with the first writer of each
// non-hub object they read that the prefix wrote; hub reads are left to
// readPass.
func (h *Next) prefixReads(txns []model.Transaction) {
	f, anchor := &h.forest, h.anchor
	for t := range txns {
		root := noRoot
		for _, obj := range txns[t].ReadSet {
			if a := anchor[obj]; a != 0 && a < hubBase {
				anchor[obj] = a | sharedBit
				if root == noRoot {
					root = f.Find(uint32(t))
				}
				root = join(f, root, a)
			}
		}
	}
}

// readPass unions every transaction with the first writer of each object it
// reads, recording hub reads.
func (h *Next) readPass(txns []model.Transaction) {
	f, anchor := &h.forest, h.anchor
	for t := range txns {
		root := noRoot
		for _, obj := range txns[t].ReadSet {
			switch a := anchor[obj]; {
			case a == 0:
			case a >= hubBase:
				h.hubReads = append(h.hubReads, hubAccess{h.who(uint32(t)), a - hubBase})
			default:
				anchor[obj] = a | sharedBit
				if root == noRoot {
					root = f.Find(uint32(t))
				}
				root = join(f, root, a)
			}
		}
	}
}
