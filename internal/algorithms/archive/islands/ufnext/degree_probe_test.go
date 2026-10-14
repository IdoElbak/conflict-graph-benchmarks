//go:build ignore

package ufnext_test

import (
	"os"
	"sort"
	"testing"

	"conflict-graph-benchmarks/internal/algorithms/islands/ufnext"
)

// TestDegreeProbe relates the prefix's non-hub shares (joins among the first
// 64 transactions, hubs by the 64/3 prefix rule) scaled to the block,
// k = 2·E·n/64², to the largest island share of the whole block, and to
// whether the block is dense (UFNEXT_STATS=1 only).
func TestDegreeProbe(t *testing.T) {
	if os.Getenv("UFNEXT_STATS") == "" {
		t.Skip("set UFNEXT_STATS=1")
	}
	for _, tf := range traceFiles {
		blocks, err := loadTrace(tf.path, tf.blocks, tf.density)
		if err != nil {
			continue
		}
		cfg := ufnext.DefaultNextConfig
		cfg.Bipartite, cfg.PrefixMinWrites = true, 3
		h := ufnext.NewNext(cfg)
		type row struct{ k, giant, density float64 }
		var rows []row
		for _, b := range blocks {
			n := len(b.Transactions)
			if n < 256 {
				continue
			}
			h.Run(b)
			largest := 0
			for _, isl := range h.Topology().Islands {
				largest = max(largest, len(isl))
			}
			hub, counts := map[uint32]bool{}, map[uint32]int{}
			for _, tx := range b.Transactions[:64] {
				for _, o := range tx.WriteSet {
					if counts[o]++; counts[o] >= 3 {
						hub[o] = true
					}
				}
			}
			written := map[uint32]bool{}
			e := 0
			for _, tx := range b.Transactions[:64] {
				for _, o := range tx.WriteSet {
					if hub[o] {
						continue
					}
					if written[o] {
						e++
					}
					written[o] = true
				}
			}
			for _, tx := range b.Transactions[:64] {
				for _, o := range tx.ReadSet {
					if !hub[o] && written[o] {
						e++
					}
				}
			}
			rows = append(rows, row{2 * float64(e) * float64(n) / 4096, float64(largest) / float64(n), density(b)})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].k < rows[j].k })
		for i := 0; i < len(rows); i += max(1, len(rows)/25) {
			t.Logf("%-8s k=%6.2f giant=%.2f density=%.3f", tf.name, rows[i].k, rows[i].giant, rows[i].density)
		}
	}
}
