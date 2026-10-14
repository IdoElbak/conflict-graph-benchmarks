package islands

import "conflict-graph-benchmarks/internal/model"

// IslandBipartite is the Union-Find island algorithm with a bipartite hub
// layer: per hub object, its writers and its readers as lists of transactions
// (model.IslandTopology.HubWriters / HubReaders); each writer conflicts with
// every other writer and every reader of the hub. A hub then costs one list
// entry per access instead of a clique among its touchers. It changes the
// output contract of the hub layer (lists instead of an explicit graph);
// otherwise it is IslandHub.
type IslandBipartite struct {
	hubIslands
}

// DefaultIslandBipartiteConfig holds the knobs the proving ground picked.
var DefaultIslandBipartiteConfig = HubConfig{
	Island:          DefaultIslandConfig,
	PrefixTxns:      64,
	PrefixMinWrites: 3,
	HubMinTxns:      64,
	TinyMax:         64,
	TinyMinRepeats:  0.5,
	DenseHubShare:   0.5,
	DenseMinTxns:    512,
	HubRowsPerUnit:  64,
}

// NewIslandBipartite returns an IslandBipartite with the given knobs
// (normally DefaultIslandBipartiteConfig). It is reusable scratch:
// steady-state calls allocate nothing.
func NewIslandBipartite(cfg HubConfig) *IslandBipartite {
	h := &IslandBipartite{}
	h.init(cfg, true)
	return h
}

// partitionBipartite is partitionHubs for the bipartite hub layer: a hub
// access records the transaction itself, so no toucher is numbered.
func (h *hubIslands) partitionBipartite(block *model.Block) {
	txns, f, anchor := block.Transactions, &h.forest, h.anchor
	h.hubWrites, h.hubReads = h.hubWrites[:0], h.hubReads[:0]
	for t := range txns {
		root := uint32(t)
		for _, obj := range txns[t].WriteSet {
			switch a := anchor[obj]; {
			case a == 0:
				anchor[obj] = root + 1
			case a >= hubBase:
				h.hubWrites = append(h.hubWrites, hubAccess{uint32(t), a - hubBase})
			default:
				anchor[obj] = a | sharedBit
				root = join(f, root, a)
			}
		}
	}
	for t := range txns {
		root := noRoot
		for _, obj := range txns[t].ReadSet {
			switch a := anchor[obj]; {
			case a == 0:
			case a >= hubBase:
				h.hubReads = append(h.hubReads, hubAccess{uint32(t), a - hubBase})
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

// buildBipartite groups the hub accesses by hub (a counting sort over the
// keys 2h = writers of hub h, 2h+1 = its readers): hub h's writers become
// HubWriters[h] and its readers HubReaders[h], in ascending transaction order.
func (h *hubIslands) buildBipartite() {
	keys := 2 * len(h.hubObjs)
	off := model.Resize(h.hubOff, keys+2)
	clear(off)
	for _, acc := range h.hubWrites {
		off[2*acc.hub+2]++
	}
	for _, acc := range h.hubReads {
		off[2*acc.hub+3]++
	}
	for k := 2; k < keys+2; k++ {
		off[k] += off[k-1]
	}
	arena := model.Resize(h.hubArena, len(h.hubWrites)+len(h.hubReads))
	for _, acc := range h.hubWrites {
		arena[off[2*acc.hub+1]] = acc.who
		off[2*acc.hub+1]++
	}
	for _, acc := range h.hubReads {
		arena[off[2*acc.hub+2]] = acc.who
		off[2*acc.hub+2]++
	}
	for hub := range len(h.hubObjs) {
		h.out.HubWriters = append(h.out.HubWriters, arena[off[2*hub]:off[2*hub+1]])
		h.out.HubReaders = append(h.out.HubReaders, arena[off[2*hub+1]:off[2*hub+2]])
	}
	h.hubOff, h.hubArena = off, arena
}
