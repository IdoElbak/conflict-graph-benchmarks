//go:build ignore

package ufnext_test

import (
	"os"
	"testing"

	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/model"
)

// TestTraceStats prints access statistics per trace (UFNEXT_STATS=1 only):
// what the partition and the kernels see.
func TestTraceStats(t *testing.T) {
	if os.Getenv("UFNEXT_STATS") == "" {
		t.Skip("set UFNEXT_STATS=1")
	}
	for _, tf := range traceFiles {
		blocks, err := loadTrace(tf.path, 2000, tf.density)
		if err != nil {
			continue
		}
		var n, w, r, firstW, deadR, objs, shared, maxDeg, bigTx, dense int
		g, idx := model.NewConflictGraph(), model.NewInvertedIndex()
		for _, b := range blocks {
			n += len(b.Transactions)
			objs += int(b.NumUniqueObjects)
			written := make([]int, b.NumUniqueObjects)
			touched := make([]int, b.NumUniqueObjects)
			for _, tx := range b.Transactions {
				w += len(tx.WriteSet)
				r += len(tx.ReadSet)
				d := len(tx.WriteSet) + len(tx.ReadSet)
				maxDeg = max(maxDeg, d)
				if d >= 32 {
					bigTx++
				}
				for _, o := range tx.WriteSet {
					if written[o] == 0 {
						firstW++
					}
					written[o]++
					touched[o]++
				}
			}
			for _, tx := range b.Transactions {
				for _, o := range tx.ReadSet {
					if written[o] == 0 {
						deadR++
					}
					touched[o]++
				}
			}
			for o := range touched {
				if written[o] > 0 && touched[o] > 1 {
					shared++
				}
			}
			competitors.BitmapConstruction(b, g, idx)
			nn := uint32(len(b.Transactions))
			if nn > 1 && float64(g.CountEdges(nn)) >= 0.1*float64(nn*(nn-1)/2) {
				dense++
			}
		}
		nb := float64(len(blocks))
		t.Logf("%-14s blocks %4d n %7.1f | W/tx %5.2f R/tx %5.2f | objs/blk %8.1f shared/blk %7.1f | firstW %4.2f deadR %4.2f | maxDeg %d tx>=32acc %d | dense>=10%% %d",
			tf.name, len(blocks), float64(n)/nb, float64(w)/float64(n), float64(r)/float64(n), float64(objs)/nb, float64(shared)/nb,
			float64(firstW)/float64(max(1, w)), float64(deadR)/float64(max(1, r)), maxDeg, bigTx, dense)
	}
}
