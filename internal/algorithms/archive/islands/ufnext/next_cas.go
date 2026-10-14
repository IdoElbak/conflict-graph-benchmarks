//go:build ignore

package ufnext

import (
	"sync/atomic"

	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// casChunkTxns is the number of transactions per unit of the concurrent
// partition. Units are static ranges, so per-unit hub buffers concatenate in
// transaction order.
const casChunkTxns = 64

// casPartition is report B's concurrent union-find partition (ConnectIt /
// Jayanti-Tarjan style): the writer pass and the reader pass each run on the
// worker pool, claiming the first writer of an object with a CAS on its
// anchor and uniting sets with a CAS on a root's parent. Linking is by index
// (the larger root goes under the smaller), so every set's root is its
// smallest transaction whatever the interleaving; finds halve paths with CAS.
// The islands are then grouped canonically (ordered by root, members
// ascending), and the hub accesses are concatenated in unit order, so the
// output is deterministic.
type casPartition struct {
	parent []uint32
	rootOf []uint32 // per transaction: its root (grouping)
	count  []uint32 // per root: members, then the fill cursor
	member []uint32 // backing array of the islands
	wHubs  [][]hubAccess
	rHubs  [][]hubAccess
}

// casFind returns x's root, halving the path with CAS.
func casFind(parent []uint32, x uint32) uint32 {
	for {
		p := atomic.LoadUint32(&parent[x])
		if p == x {
			return x
		}
		gp := atomic.LoadUint32(&parent[p])
		if gp != p {
			atomic.CompareAndSwapUint32(&parent[x], p, gp)
		}
		x = gp
	}
}

// casUnite merges the sets of x and y and returns the merged root.
func casUnite(parent []uint32, x, y uint32) uint32 {
	for {
		x, y = casFind(parent, x), casFind(parent, y)
		if x == y {
			return x
		}
		if x < y {
			x, y = y, x
		}
		if atomic.CompareAndSwapUint32(&parent[x], x, y) {
			return y
		}
	}
}

// partitionConcurrent partitions the block on the worker pool into h.out's
// islands (not the forest) and records the hub accesses, numbering the
// touchers afterwards in the same order as the sequential partition.
func (h *Next) partitionConcurrent(block *model.Block) {
	p := &h.cas
	txns, anchor := block.Transactions, h.anchor
	n := len(txns)
	p.parent = model.Resize(p.parent, n)
	for i := range p.parent {
		p.parent[i] = uint32(i)
	}
	units := (n + casChunkTxns - 1) / casChunkTxns
	for len(p.wHubs) < units {
		p.wHubs, p.rHubs = append(p.wHubs, nil), append(p.rHubs, nil)
	}
	parent := p.parent
	concurrency.ParallelFor(units, 1, func(_, lo, _ int) {
		p.wHubs[lo] = casWrites(txns, lo, parent, anchor, p.wHubs[lo][:0])
	})
	concurrency.ParallelFor(units, 1, func(_, lo, _ int) {
		p.rHubs[lo] = casReads(txns, lo, parent, anchor, p.rHubs[lo][:0])
	})
	h.gatherHubAccesses(units, len(txns))
	p.group(&h.out, n)
}

// casWrites runs the writer pass over unit u's transactions.
func casWrites(txns []model.Transaction, u int, parent, anchor []uint32, hubs []hubAccess) []hubAccess {
	for t := u * casChunkTxns; t < min(len(txns), (u+1)*casChunkTxns); t++ {
		root := uint32(t)
		for _, obj := range txns[t].WriteSet {
			a := atomic.LoadUint32(&anchor[obj])
			for a == 0 || a >= countTag {
				if atomic.CompareAndSwapUint32(&anchor[obj], a, uint32(t)+1) {
					break
				}
				a = atomic.LoadUint32(&anchor[obj])
			}
			switch {
			case a == 0 || a >= countTag:
				// t claimed the object: it is its first writer.
			case a >= hubBase:
				hubs = append(hubs, hubAccess{uint32(t), a - hubBase})
			default:
				if a&sharedBit == 0 {
					atomic.OrUint32(&anchor[obj], sharedBit)
				}
				root = casUnite(parent, root, a&^sharedBit-1)
			}
		}
	}
	return hubs
}

// casReads runs the reader pass over unit u's transactions.
func casReads(txns []model.Transaction, u int, parent, anchor []uint32, hubs []hubAccess) []hubAccess {
	for t := u * casChunkTxns; t < min(len(txns), (u+1)*casChunkTxns); t++ {
		root := uint32(t)
		for _, obj := range txns[t].ReadSet {
			switch a := atomic.LoadUint32(&anchor[obj]); {
			case a == 0:
			case a >= hubBase:
				hubs = append(hubs, hubAccess{uint32(t), a - hubBase})
			default:
				if a&sharedBit == 0 {
					atomic.OrUint32(&anchor[obj], sharedBit)
				}
				root = casUnite(parent, root, a&^sharedBit-1)
			}
		}
	}
	return hubs
}

// gatherHubAccesses concatenates the units' hub accesses in transaction order
// and, for the matrix hub layer, renames each transaction by its toucher
// index, numbered in order of first hub access as the sequential pass does.
func (h *Next) gatherHubAccesses(units, n int) {
	h.hubWrites, h.hubReads = h.hubWrites[:0], h.hubReads[:0]
	for u := range units {
		h.hubWrites = append(h.hubWrites, h.cas.wHubs[u]...)
		h.hubReads = append(h.hubReads, h.cas.rHubs[u]...)
	}
	if h.ncfg.Bipartite || len(h.hubObjs) == 0 {
		return
	}
	h.toucherOf = model.Resize(h.toucherOf, n)
	clear(h.toucherOf)
	for i := range h.hubWrites {
		h.hubWrites[i].who = h.toucher(h.hubWrites[i].who)
	}
	for i := range h.hubReads {
		h.hubReads[i].who = h.toucher(h.hubReads[i].who)
	}
}

// group records every set of two or more transactions as an island of out,
// islands ordered by root and members ascending (a counting sort by root).
func (p *casPartition) group(out *model.IslandTopology, n int) {
	p.rootOf = model.Resize(p.rootOf, n)
	p.count = model.Resize(p.count, n)
	p.member = model.Resize(p.member, n)
	clear(p.count)
	for t := range n {
		r := casFind(p.parent, uint32(t))
		p.rootOf[t] = r
		p.count[r]++
	}
	pos := uint32(0)
	for r := range n {
		if c := p.count[r]; c >= 2 {
			p.count[r] = pos
			out.Islands = append(out.Islands, p.member[pos:pos+c:pos+c])
			pos += c
		} else {
			p.count[r] = ^uint32(0)
		}
	}
	for t := range n {
		if c := &p.count[p.rootOf[t]]; *c != ^uint32(0) {
			p.member[*c] = uint32(t)
			*c++
		}
	}
}
