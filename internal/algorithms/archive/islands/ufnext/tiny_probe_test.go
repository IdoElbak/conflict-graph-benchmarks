//go:build ignore

package ufnext_test

import (
	"os"
	"testing"

	"conflict-graph-benchmarks/internal/digestion"
)

// TestTinyProbe prints, for blocks of at most 64 transactions, objects per
// transaction and repeats per transaction (UFNEXT_STATS=1 only).
func TestTinyProbe(t *testing.T) {
	if os.Getenv("UFNEXT_STATS") == "" {
		t.Skip("set UFNEXT_STATS=1")
	}
	for _, tf := range []struct{ name, path string }{{"Sui", digestion.SuiCleanFilePath}, {"YCSB", digestion.YCSBFilePath}, {"TPC", digestion.TPCFilePath}} {
		blocks, err := loadTrace(tf.path, 2000, nil)
		if err != nil {
			continue
		}
		hist := map[string]int{}
		for _, b := range blocks {
			n := len(b.Transactions)
			if n > 64 {
				continue
			}
			acc := 0
			for _, tx := range b.Transactions {
				acc += len(tx.WriteSet) + len(tx.ReadSet)
			}
			opt := float64(b.NumUniqueObjects) / float64(n)
			rep := float64(acc-int(b.NumUniqueObjects)) / float64(n)
			d := density(b)
			key := "sparse"
			if d >= 0.01 {
				key = "dense"
			}
			hist[key]++
			t.Logf("%s n=%d objs/tx=%.2f rep/tx=%.2f acc/tx=%.2f density=%.3f", tf.name, n, opt, rep, float64(acc)/float64(n), d)
		}
		t.Logf("%s %v", tf.name, hist)
	}
}
