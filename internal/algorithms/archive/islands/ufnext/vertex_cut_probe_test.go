//go:build ignore

package ufnext_test

import (
	"os"
	"slices"
	"testing"

	"conflict-graph-benchmarks/internal/algorithms/islands"
	"conflict-graph-benchmarks/internal/model"
)

// TestVertexCutProbe measures how much cutting high-degree transactions out
// of the partition (report B's transaction-level vertex cut) would shrink
// IslandLeanHub's largest island: the median largest-island share with no cut
// and with the top 1% / 5% of transactions by access count removed.
func TestVertexCutProbe(t *testing.T) {
	if os.Getenv("UFNEXT_STATS") == "" {
		t.Skip("set UFNEXT_STATS=1")
	}
	for _, tf := range traceFiles {
		blocks, err := loadTrace(tf.path, 300, tf.density)
		if err != nil {
			continue
		}
		hub := islands.NewIslandLeanHub(islands.DefaultIslandConfig, islands.DefaultHubRule())
		var shares [3][]float64
		for _, b := range blocks {
			n := len(b.Transactions)
			if n < 64 {
				continue
			}
			for i, cut := range []float64{0, 0.01, 0.05} {
				hub.Run(cutTop(b, int(cut*float64(n))))
				shares[i] = append(shares[i], float64(hub.Topology().Stats().MaxSize)/float64(n))
			}
		}
		if len(shares[0]) == 0 {
			continue
		}
		med := func(s []float64) float64 { slices.Sort(s); return s[len(s)/2] }
		t.Logf("%-14s largest island share: no cut %.2f | top 1%% cut %.2f | top 5%% cut %.2f", tf.name, med(shares[0]), med(shares[1]), med(shares[2]))
	}
}

// cutTop returns a copy of b whose k transactions with the most accesses
// have empty access sets.
func cutTop(b *model.Block, k int) *model.Block {
	c := &model.Block{NumUniqueObjects: b.NumUniqueObjects, Transactions: slices.Clone(b.Transactions)}
	order := make([]int, len(c.Transactions))
	for i := range order {
		order[i] = i
	}
	deg := func(i int) int { return len(b.Transactions[i].WriteSet) + len(b.Transactions[i].ReadSet) }
	slices.SortFunc(order, func(x, y int) int { return deg(y) - deg(x) })
	for _, i := range order[:k] {
		c.Transactions[i].ReadSet, c.Transactions[i].WriteSet = nil, nil
	}
	return c
}
