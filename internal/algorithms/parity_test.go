package algorithms_test

import (
	"math/rand/v2"
	"slices"
	"testing"

	"conflict-graph-benchmarks/internal/algorithms"
	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/algorithms/islands"
	"conflict-graph-benchmarks/internal/model"
)

// randomBlock returns n transactions over numObjects objects with disjoint,
// sorted read and write sets (as the Manager builds them). Squaring a uniform
// draw makes low object ids hot, so islands range from pairs to giants. With
// feeHub, every transaction also writes object 0, like Ethereum's fee
// recipient.
func randomBlock(rng *rand.Rand, n, numObjects int, feeHub bool) *model.Block {
	block := &model.Block{NumUniqueObjects: uint32(numObjects), Transactions: make([]model.Transaction, n)}
	for t := range block.Transactions {
		var reads, writes []uint32
		if feeHub {
			writes = append(writes, 0)
		}
		for range 1 + rng.IntN(8) {
			f := rng.Float64()
			obj := uint32(f * f * float64(numObjects))
			if slices.Contains(reads, obj) || slices.Contains(writes, obj) {
				continue
			}
			if rng.IntN(3) == 0 {
				writes = append(writes, obj)
			} else {
				reads = append(reads, obj)
			}
		}
		slices.Sort(reads)
		slices.Sort(writes)
		block.Transactions[t] = model.Transaction{ID: uint32(t), ReadSet: reads, WriteSet: writes}
	}
	return block
}

// TestAlgorithmsMatchNaiveSequential checks every timed algorithm bit for bit
// against the definitional reference on seeded random blocks, along with the
// island algorithms with each of their paths forced on every block it can
// take: parallel build, pairwise islands, many hubs (the complete hub matrix
// included), the giant-island kernel with every object as pairs and as rows,
// the tiny path and the dense fallback.
func TestAlgorithmsMatchNaiveSequential(t *testing.T) {
	type islandVariant interface {
		Run(*model.Block) (int, int, float64)
		Topology() *model.IslandTopology
	}
	manyHubs := func(c *islands.HubConfig) { c.PrefixTxns, c.PrefixMinWrites = 16, 2 }
	forceParallel := func(c *islands.HubConfig) { c.Island.SequentialMax, c.Island.ParallelWords = 0, 0 }
	cases := []struct {
		name   string
		change func(*islands.HubConfig)
	}{
		{"default", func(*islands.HubConfig) {}},
		{"parallel", forceParallel},
		{"pairwise 16", func(c *islands.HubConfig) { c.Island.PairwiseMax = 16 }},
		{"many hubs parallel", func(c *islands.HubConfig) { manyHubs(c); forceParallel(c) }},
		{"strided prefix, hub rows 8", func(c *islands.HubConfig) { c.PrefixStrided, c.HubRowsPerUnit = true, 8; manyHubs(c); forceParallel(c) }},
		{"no hubs", func(c *islands.HubConfig) { c.PrefixMinWrites = 1 << 20 }},
		{"no giant kernel", func(c *islands.HubConfig) { c.Island.GiantShare = 0 }},
		{"giant pairs", func(c *islands.HubConfig) { c.Island.GiantShare, c.Island.JoinDense = 0.01, 1e9 }},
		{"giant rows parallel", func(c *islands.HubConfig) { c.Island.GiantShare, c.Island.JoinDense = 0.01, 1e-9; forceParallel(c) }},
		{"always tiny", func(c *islands.HubConfig) { c.TinyMinRepeats = 0 }},
		{"no tiny", func(c *islands.HubConfig) { c.TinyMax = 0 }},
		{"all dense", func(c *islands.HubConfig) { c.TinyMax, c.DenseHubShare, c.DenseMinTxns = 32, 1e-9, 0 }},
		{"dense share 0.3 many hubs", func(c *islands.HubConfig) { c.DenseHubShare, c.DenseMinTxns = 0.3, 100; manyHubs(c) }},
	}
	type islandCase struct {
		name    string
		variant islandVariant
	}
	var islandCases []islandCase
	for _, c := range cases {
		cfg := islands.DefaultIslandHubConfig
		c.change(&cfg)
		islandCases = append(islandCases,
			islandCase{"IslandHub " + c.name, islands.NewIslandHub(cfg)},
			islandCase{"IslandBipartite " + c.name, islands.NewIslandBipartite(cfg)})
	}

	// The competitors, with HybridJoin on narrow blocks too, and with every
	// object as pairs and as dense.
	hybridJoin := competitors.HybridJoinConfig{MinWords: 1, DenseFactor: 1}
	joinLists, joinDense := hybridJoin, hybridJoin
	joinLists.DenseFactor, joinDense.DenseFactor = 1e9, 0
	matrices := []struct {
		name string
		run  func(*model.Block, *model.ConflictGraph)
	}{
		{"HybridJoin", competitors.NewHybridJoin(competitors.DefaultHybridJoinConfig).Run},
		{"HybridJoin narrow", competitors.NewHybridJoin(hybridJoin).Run},
		{"HybridJoin all pairs", competitors.NewHybridJoin(joinLists).Run},
		{"HybridJoin all dense", competitors.NewHybridJoin(joinDense).Run},
		{"BitmapTiny", competitors.NewBitmapTiny().Run},
		{"HybridJoinTiny", competitors.NewHybridJoinTiny(competitors.DefaultHybridJoinConfig).Run},
	}

	want, got := model.NewConflictGraph(), model.NewConflictGraph()
	index := model.NewInvertedIndex()
	rng := rand.New(rand.NewPCG(4, 2026))
	for blockIdx := range 81 {
		n := 1 + rng.IntN(500)
		if blockIdx == 80 {
			n = 0 // an empty block, as the Sui trace has
		}
		block := randomBlock(rng, n, 1+rng.IntN(max(1, 3*n)), blockIdx%5 == 0)
		size := uint32(n)
		algorithms.NaiveSequential(block, want)
		check := func(name string) {
			t.Helper()
			if !want.AreGraphsEqual(got, size) {
				t.Fatalf("%s: block %d (%d txns) differs from NaiveSequential", name, blockIdx, n)
			}
		}
		competitors.BitmapConstruction(block, got, index)
		check("Bitmap")
		for _, m := range matrices {
			got.Reset(size)
			fillGarbage(got, size)
			m.run(block, got)
			check(m.name)
		}
		for _, island := range islandCases {
			island.variant.Run(block)
			got.Reset(size)
			island.variant.Topology().Reconstruct(got)
			check(island.name)
		}
	}
}

// fillGarbage sets every bit of the block's rows, so an algorithm that leaves
// a word of its output unwritten fails the check.
func fillGarbage(graph *model.ConflictGraph, n uint32) {
	for t := range n {
		row := graph.Row(t)
		for k := range row {
			row[k] = ^uint64(0)
		}
	}
}
