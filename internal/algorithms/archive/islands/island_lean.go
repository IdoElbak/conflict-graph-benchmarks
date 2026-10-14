//go:build ignore

package islands

import "conflict-graph-benchmarks/internal/model"

// IslandLean builds the conflict graph as islands: it partitions the block's
// transactions with a union-find into islands that cannot conflict with each
// other, then builds one small local matrix per island. Its output is a
// model.IslandTopology (here without a hub layer), which the downstream
// pipeline can graph-color island by island.
//
// The front half does only what the islands need:
//   - two passes over the accesses partition the block; each transaction's
//     root is tracked across its own accesses, so a union costs one Find;
//   - islands are read off the circular member lists of the forest, without a
//     Find per transaction or a counting sort;
//   - only live objects enter the index and the sweeps: written objects that
//     a second transaction touches (any other object cannot produce an edge);
//   - each island's live accesses are compacted once without branches, then
//     its index rows are built, swept and wiped while they are hot; islands of
//     at most 64 members use one-word rows end to end.
type IslandLean struct {
	islandCore
}

// NewIslandLean returns an IslandLean with the given knobs (normally
// DefaultIslandConfig). It is reusable scratch: steady-state calls allocate
// nothing.
func NewIslandLean(cfg IslandConfig) *IslandLean {
	return &IslandLean{islandCore{cfg: cfg}}
}

// Run builds the block's topology and returns (islands, largest, mean size).
func (l *IslandLean) Run(block *model.Block) (int, int, float64) {
	l.begin(block)
	partitionByAnchors(block, &l.forest, l.anchor)
	return l.buildIslands(block)
}
