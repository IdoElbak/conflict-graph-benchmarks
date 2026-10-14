package islands

import (
	"math/bits"

	"conflict-graph-benchmarks/internal/model"
)

// IslandHub is the Union-Find island algorithm with a matrix hub layer: the
// hubs' conflicts are a bit-matrix over the transactions touching a hub
// (model.IslandTopology.HubGraph), so every edge of the block is an explicit
// bit. On top of the island core it routes the two block shapes a partition
// cannot pay for: tiny blocks to the one-word kernel, dense ones to
// HybridJoin, each built as a single island.
type IslandHub struct {
	hubIslands
}

// DefaultIslandHubConfig holds the knobs the proving ground picked.
var DefaultIslandHubConfig = HubConfig{
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

// NewIslandHub returns an IslandHub with the given knobs (normally
// DefaultIslandHubConfig). It is reusable scratch: steady-state calls
// allocate nothing.
func NewIslandHub(cfg HubConfig) *IslandHub {
	h := &IslandHub{}
	h.init(cfg, false)
	return h
}

// partitionHubs unions every transaction with the first writer of each
// object it writes, then of each object it reads, in two passes over the
// accesses, and marks every object a second transaction touches as shared.
// Reads of never-written objects union nothing. Each transaction's root is
// tracked across its own accesses, so a union costs one Find, not two. Hub
// accesses union nothing and are recorded for the matrix hub layer under the
// toucher's index.
func (h *hubIslands) partitionHubs(block *model.Block) {
	txns, f, anchor := block.Transactions, &h.forest, h.anchor
	h.hubWrites, h.hubReads = h.hubWrites[:0], h.hubReads[:0]
	h.toucherOf = model.Resize(h.toucherOf, len(txns))
	clear(h.toucherOf)
	for t := range txns {
		root := uint32(t)
		for _, obj := range txns[t].WriteSet {
			switch a := anchor[obj]; {
			case a == 0:
				anchor[obj] = root + 1
			case a >= hubBase:
				h.hubWrites = append(h.hubWrites, hubAccess{h.toucher(uint32(t)), a - hubBase})
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
				h.hubReads = append(h.hubReads, hubAccess{h.toucher(uint32(t)), a - hubBase})
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

// toucher returns t's toucher index, numbering t on its first hub access.
func (h *hubIslands) toucher(t uint32) uint32 {
	if q := h.toucherOf[t]; q != 0 {
		return q - 1
	}
	q := uint32(len(h.out.HubTouchers))
	h.out.HubTouchers = append(h.out.HubTouchers, t)
	h.toucherOf[t] = q + 1
	return q
}

// prepareHubLayer sizes the hub matrix (one row per toucher), builds the hub
// objects' writer and reader bitsets over the touchers and checks whether the
// matrix is complete.
func (h *hubIslands) prepareHubLayer() {
	touchers := uint32(len(h.out.HubTouchers))
	hubs := uint32(len(h.hubObjs))
	stride := model.WordsPerRow(touchers)
	h.out.HubWidth, h.hubStride = stride, stride
	h.hubW = model.Resize(h.hubW, int(hubs*stride))
	h.hubR = model.Resize(h.hubR, int(hubs*stride))
	clear(h.hubW)
	clear(h.hubR)
	for _, acc := range h.hubWrites {
		h.hubW[acc.hub*stride+acc.who/64] |= 1 << (acc.who % 64)
	}
	for _, acc := range h.hubReads {
		h.hubR[acc.hub*stride+acc.who/64] |= 1 << (acc.who % 64)
	}
	h.hubGraph = model.Resize(h.hubGraph, int(touchers*stride))
	h.out.HubGraph = h.hubGraph
	h.complete = h.hasUniversalHub(touchers)
}

// hasUniversalHub reports whether some hub is written by every toucher.
func (h *hubIslands) hasUniversalHub(touchers uint32) bool {
	stride := h.hubStride
	for hub := range uint32(len(h.hubObjs)) {
		writers := 0
		for _, word := range h.hubW[hub*stride : (hub+1)*stride] {
			writers += bits.OnesCount64(word)
		}
		if uint32(writers) == touchers {
			return true
		}
	}
	return false
}

// fillCompleteRows writes rows [lo, hi) of a complete hub matrix.
func (h *hubIslands) fillCompleteRows(lo, hi uint32) {
	graph, stride := h.hubGraph, h.hubStride
	touchers := uint32(len(h.out.HubTouchers))
	last := ^uint64(0) >> ((64 - touchers%64) % 64)
	for q := lo; q < hi; q++ {
		row := graph[q*stride : (q+1)*stride]
		for k := range row {
			row[k] = ^uint64(0)
		}
		row[stride-1] = last
		row[q/64] &^= 1 << (q % 64)
	}
}

// buildHubLayer builds the hub matrix inline.
func (h *hubIslands) buildHubLayer() {
	h.prepareHubLayer()
	touchers := uint32(len(h.out.HubTouchers))
	if h.complete {
		h.fillCompleteRows(0, touchers)
		return
	}
	graph, stride := h.hubGraph, h.hubStride
	clear(graph)
	for _, acc := range h.hubWrites {
		row := graph[acc.who*stride : (acc.who+1)*stride]
		writers := h.hubW[acc.hub*stride:][:len(row)]
		readers := h.hubR[acc.hub*stride:][:len(row)]
		for k := range row {
			row[k] |= writers[k] | readers[k]
		}
	}
	for _, acc := range h.hubReads {
		row := graph[acc.who*stride : (acc.who+1)*stride]
		writers := h.hubW[acc.hub*stride:][:len(row)]
		for k := range row {
			row[k] |= writers[k]
		}
	}
	for q := range touchers {
		graph[q*stride+q/64] &^= 1 << (q % 64)
	}
}

// planHubRows prepares the hub layer for the parallel path.
func (h *hubIslands) planHubRows() {
	h.prepareHubLayer()
	touchers := len(h.out.HubTouchers)
	h.wOff, h.wHubs = groupByToucher(h.hubWrites, touchers, h.wOff, h.wHubs)
	h.rOff, h.rHubs = groupByToucher(h.hubReads, touchers, h.rOff, h.rHubs)
	for lo := 0; lo < touchers; lo += h.hcfg.HubRowsPerUnit {
		hi := min(lo+h.hcfg.HubRowsPerUnit, touchers)
		h.extraUnits = append(h.extraUnits, islandUnit{kind: unitExtra, lo: uint32(lo), hi: uint32(hi)})
	}
}

// groupByToucher counting-sorts accesses by toucher: the hubs of toucher q end
// up in hubIDs[off[q]:off[q+1]].
func groupByToucher(accesses []hubAccess, touchers int, off, hubIDs []uint32) ([]uint32, []uint32) {
	off = model.Resize(off, touchers+2)
	clear(off)
	for _, acc := range accesses {
		off[acc.who+2]++
	}
	for q := 2; q < touchers+2; q++ {
		off[q] += off[q-1]
	}
	hubIDs = model.Resize(hubIDs, len(accesses))
	for _, acc := range accesses {
		hubIDs[off[acc.who+1]] = acc.hub
		off[acc.who+1]++
	}
	return off, hubIDs
}

// sweepHubRows writes rows [lo, hi) of the hub matrix (parallel path).
func (h *hubIslands) sweepHubRows(lo, hi uint32) {
	if h.complete {
		h.fillCompleteRows(lo, hi)
		return
	}
	graph, stride := h.hubGraph, h.hubStride
	for q := lo; q < hi; q++ {
		row := graph[q*stride : (q+1)*stride]
		clear(row)
		for _, hub := range h.wHubs[h.wOff[q]:h.wOff[q+1]] {
			writers := h.hubW[hub*stride:][:len(row)]
			readers := h.hubR[hub*stride:][:len(row)]
			for k := range row {
				row[k] |= writers[k] | readers[k]
			}
		}
		for _, hub := range h.rHubs[h.rOff[q]:h.rOff[q+1]] {
			writers := h.hubW[hub*stride:][:len(row)]
			for k := range row {
				row[k] |= writers[k]
			}
		}
		row[q/64] &^= 1 << (q % 64)
	}
}
