//go:build ignore

package ufnext_test

import (
	"os"
	"slices"
	"testing"

	"conflict-graph-benchmarks/internal/model"
)

// TestDenseProbe prints, per trace and density band, what the 64-transaction
// prefix of the hub rule sees: the share of prefix transactions that write a
// hub, that touch one (read or write), and the prefix's own density.
func TestDenseProbe(t *testing.T) {
	if os.Getenv("UFNEXT_STATS") == "" {
		t.Skip("set UFNEXT_STATS=1")
	}
	for _, tf := range traceFiles {
		blocks, err := loadTrace(tf.path, 300, nil)
		if err != nil {
			continue
		}
		type row struct{ d, w, touch, pd float64 }
		var rows []row
		for _, b := range blocks {
			if len(b.Transactions) < 256 {
				continue
			}
			p := b.Transactions[:64]
			counts := map[uint32]int{}
			for _, tx := range p {
				for _, o := range tx.WriteSet {
					counts[o]++
				}
			}
			var w, touch float64
			for _, tx := range p {
				hw, ht := false, false
				for _, o := range tx.WriteSet {
					hw = hw || counts[o] >= 4
				}
				for _, o := range tx.ReadSet {
					ht = ht || counts[o] >= 4
				}
				if hw {
					w++
				}
				if hw || ht {
					touch++
				}
			}
			pb := &model.Block{Transactions: p, NumUniqueObjects: b.NumUniqueObjects}
			rows = append(rows, row{density(b), w / 64, touch / 64, density(pb)})
		}
		for _, band := range [][2]float64{{0, 0.01}, {0.01, 0.03}, {0.03, 0.1}, {0.1, 0.2}, {0.2, 1.01}} {
			var ws, ts, ps []float64
			for _, r := range rows {
				if band[0] <= r.d && r.d < band[1] {
					ws, ts, ps = append(ws, r.w), append(ts, r.touch), append(ps, r.pd)
				}
			}
			if len(ws) == 0 {
				continue
			}
			med := func(s []float64) float64 { slices.Sort(s); return s[len(s)/2] }
			t.Logf("%-14s density [%.2f,%.2f) %3d blocks: prefix hub writers %.2f, hub touchers %.2f, prefix density %.3f",
				tf.name, band[0], band[1], len(ws), med(ws), med(ts), med(ps))
		}
	}
}
