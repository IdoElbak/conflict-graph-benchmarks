package digestion

import (
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

// TestSyntheticDeterminism: the same spec must always produce the same block.
func TestSyntheticDeterminism(t *testing.T) {
	spec := blockSpec{Number: 7, Size: 300, TargetDensity: 0.03}
	builders := map[string]blockBuilder{"YCSB": buildYCSBBlock, "TPC": buildTPCBlock}
	for name, build := range builders {
		a, knobA, densA := build(spec, newEdgeCounter())
		b, knobB, densB := build(spec, newEdgeCounter())
		if !reflect.DeepEqual(a, b) || knobA != knobB || densA != densB {
			t.Errorf("%s: two builds of the same spec differ", name)
		}
		if math.Abs(densA/spec.TargetDensity-1) > 0.25 {
			t.Errorf("%s: density %.4f far from target %.4f", name, densA, spec.TargetDensity)
		}
	}
	if !reflect.DeepEqual(planBlocks(YCSBSeed), planBlocks(YCSBSeed)) {
		t.Error("block plan is not deterministic")
	}
}

// TestZipfSampler checks the rank-1/rank-2 frequency ratio (2^theta) and bounds.
func TestZipfSampler(t *testing.T) {
	const n, draws = 1000, 400_000
	for _, theta := range []float64{0, 0.99, 2} {
		z := newZipfSampler(n, theta)
		rng := rand.New(rand.NewPCG(1, 2))
		counts := make([]int, n+1)
		for range draws {
			k := z.sample(rng)
			if k < 1 || k > n {
				t.Fatalf("theta=%v: rank %d out of [1, %d]", theta, k, n)
			}
			counts[k]++
		}
		got, want := float64(counts[1])/float64(counts[2]), math.Pow(2, theta)
		if math.Abs(got/want-1) > 0.1 {
			t.Errorf("theta=%v: P(1)/P(2) = %.3f, want %.3f", theta, got, want)
		}
	}
}

// TestEdgeCounter compares the bitset edge count against brute-force pairs.
func TestEdgeCounter(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	ec := newEdgeCounter()
	for trial := range 50 {
		txns := make([]synTxn, 2+rng.IntN(150))
		for i := range txns {
			for range rng.IntN(6) {
				txns[i].add(uint64(rng.IntN(40)), rng.IntN(2) == 0)
			}
		}
		edges := 0
		for i := range txns {
			for j := i + 1; j < len(txns); j++ {
				if conflicts(txns[i], txns[j]) {
					edges++
				}
			}
		}
		n := len(txns)
		if want := float64(edges) / float64(n*(n-1)/2); ec.density(txns) != want {
			t.Fatalf("trial %d: density %v, want %v", trial, ec.density(txns), want)
		}
	}
}

// conflicts is the brute-force W-W / R-W check.
func conflicts(a, b synTxn) bool {
	for _, w := range a.writes {
		if slices.Contains(b.writes, w) || slices.Contains(b.reads, w) {
			return true
		}
	}
	for _, w := range b.writes {
		if slices.Contains(a.reads, w) {
			return true
		}
	}
	return false
}
