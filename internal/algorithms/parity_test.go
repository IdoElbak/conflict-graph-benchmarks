package algorithms_test

import (
	"math/rand/v2"
	"slices"
	"testing"

	"conflict-graph-benchmarks/internal/algorithms"
	"conflict-graph-benchmarks/internal/algorithms/uflab"
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
	parallel := algorithms.DefaultIslandConfig
	parallel.SequentialMax, parallel.ParallelWords = 0, 0
	pairwise := algorithms.DefaultIslandConfig
	pairwise.PairwiseMax = 16
	islands := []struct {
		name    string
		variant interface {
			Run(*model.Block) (int, int, float64)
			Topology() *model.IslandTopology
		}
	}{
		{"IslandLean", algorithms.NewIslandLean(algorithms.DefaultIslandConfig)},
		{"IslandLeanHub", algorithms.NewIslandLeanHub(algorithms.DefaultIslandConfig, algorithms.DefaultHubRule())},
		{"IslandLean parallel", algorithms.NewIslandLean(parallel)},
		{"IslandLean pairwise 16", algorithms.NewIslandLean(pairwise)},
		{"prefix hubs parallel", algorithms.NewIslandLeanHub(parallel, &algorithms.PrefixHubs{Txns: 16, MinWrites: 4})},
		{"writers hubs", algorithms.NewIslandLeanHub(algorithms.DefaultIslandConfig, &uflab.CountedHubs{Count: uflab.Writers, Threshold: 3})},
		{"weighted hubs", algorithms.NewIslandLeanHub(pairwise, &uflab.CountedHubs{Count: uflab.Weighted, Threshold: 12, WriteWeight: 4})},
		{"split hubs parallel", algorithms.NewIslandLeanHub(parallel, &uflab.CountedHubs{Count: uflab.Split, Threshold: 3, ReadThreshold: 6})},
		{"sampled hubs", algorithms.NewIslandLeanHub(algorithms.DefaultIslandConfig, &uflab.CountedHubs{Count: uflab.Sampled, Threshold: 4, SampleEvery: 2})},
	}

	want, got := model.NewConflictGraph(), model.NewConflictGraph()
	index, bucket := model.NewInvertedIndex(), model.NewIdBucket()
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
		algorithms.BitmapConstruction(block, got, index)
		check("Bitmap")
		algorithms.IDBucketConstruction(block, got, bucket)
		check("IDBucket")
		for _, island := range islands {
			island.variant.Run(block)
			got.Reset(size)
			island.variant.Topology().Reconstruct(got)
			check(island.name)
		}
	}
}
