package algorithms_test

import (
	"math/rand/v2"
	"slices"
	"testing"

	"conflict-graph-benchmarks/internal/algorithms"
	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/algorithms/islands"
	"conflict-graph-benchmarks/internal/algorithms/islands/uflab"
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
// island algorithms' parallel path forced on every block and every hub rule
// of the proving ground (the complete hub matrix included).
func TestAlgorithmsMatchNaiveSequential(t *testing.T) {
	parallel := islands.DefaultIslandConfig
	parallel.SequentialMax, parallel.ParallelWords = 0, 0
	pairwise := islands.DefaultIslandConfig
	pairwise.PairwiseMax = 16
	islandJoin, islandJoinParallel, islandJoinPairs, islandJoinDense := islands.DefaultIslandConfig, parallel, islands.DefaultIslandConfig, parallel
	islandJoin.JoinDense, islandJoinParallel.JoinDense, islandJoinPairs.JoinDense, islandJoinDense.JoinDense = 1, 1, 1e9, 1e-9
	islandJoinMixed := islandJoinParallel
	islandJoinMixed.JoinMinWidth = 3 // islands of 65..128 members keep the Bitmap-style kernel
	islands := []struct {
		name    string
		variant interface {
			Run(*model.Block) (int, int, float64)
			Topology() *model.IslandTopology
		}
	}{
		{"IslandLean", islands.NewIslandLean(islands.DefaultIslandConfig)},
		{"IslandLeanHub", islands.NewIslandLeanHub(islands.DefaultIslandConfig, islands.DefaultHubRule())},
		{"IslandLean parallel", islands.NewIslandLean(parallel)},
		{"IslandLean pairwise 16", islands.NewIslandLean(pairwise)},
		{"prefix hubs parallel", islands.NewIslandLeanHub(parallel, &islands.PrefixHubs{Txns: 16, MinWrites: 4})},
		{"writers hubs", islands.NewIslandLeanHub(islands.DefaultIslandConfig, &uflab.CountedHubs{Count: uflab.Writers, Threshold: 3})},
		{"weighted hubs", islands.NewIslandLeanHub(pairwise, &uflab.CountedHubs{Count: uflab.Weighted, Threshold: 12, WriteWeight: 4})},
		{"split hubs parallel", islands.NewIslandLeanHub(parallel, &uflab.CountedHubs{Count: uflab.Split, Threshold: 3, ReadThreshold: 6})},
		{"IslandLean join", islands.NewIslandLean(islandJoin)},
		{"IslandLean join parallel", islands.NewIslandLean(islandJoinParallel)},
		{"IslandLeanHub join mixed parallel", islands.NewIslandLeanHub(islandJoinMixed, &islands.PrefixHubs{Txns: 16, MinWrites: 4})},
		{"IslandLean join all pairs", islands.NewIslandLean(islandJoinPairs)},
		{"IslandLeanHub join all dense parallel", islands.NewIslandLeanHub(islandJoinDense, islands.DefaultHubRule())},
		{"IslandLeanHub join", islands.NewIslandLeanHub(islandJoin, islands.DefaultHubRule())},
		{"sampled hubs", islands.NewIslandLeanHub(islands.DefaultIslandConfig, &uflab.CountedHubs{Count: uflab.Sampled, Threshold: 4, SampleEvery: 2})},
	}

	// The competitors: defaults, SortJoin's parallel path forced, and
	// HybridJoin on narrow blocks too, with every object as pairs and as dense.
	joinParallel := competitors.DefaultSortJoinConfig
	joinParallel.SequentialMax = 0
	hybridJoin := competitors.HybridJoinConfig{MinWords: 1, DenseFactor: 1}
	joinLists, joinDense := hybridJoin, hybridJoin
	joinLists.DenseFactor, joinDense.DenseFactor = 1e9, 0
	matrices := []struct {
		name string
		run  func(*model.Block, *model.ConflictGraph)
	}{
		{"SortJoin", competitors.NewSortJoin(competitors.DefaultSortJoinConfig).Run},
		{"SortJoin parallel", competitors.NewSortJoin(joinParallel).Run},
		{"HybridJoin", competitors.NewHybridJoin(competitors.DefaultHybridJoinConfig).Run},
		{"HybridJoin narrow", competitors.NewHybridJoin(hybridJoin).Run},
		{"HybridJoin all pairs", competitors.NewHybridJoin(joinLists).Run},
		{"HybridJoin all dense", competitors.NewHybridJoin(joinDense).Run},
	}

	want, got := model.NewConflictGraph(), model.NewConflictGraph()
	index := model.NewInvertedIndex()
	rng := rand.New(rand.NewPCG(4, 2026))
	for blockIdx := range 80 {
		n := 1 + rng.IntN(500)
		block := randomBlock(rng, n, 1+rng.IntN(3*n), blockIdx%5 == 0)
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
		for _, island := range islands {
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
