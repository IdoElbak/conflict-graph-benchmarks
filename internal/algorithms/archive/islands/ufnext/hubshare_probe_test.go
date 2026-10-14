//go:build ignore

package ufnext_test

import (
	"fmt"
	"os"
	"testing"
)

// TestHubShareProbe prints, per trace and density band, the share of the
// 64-transaction prefix writing a hub (3 prefix writes), counting only hubs
// written by fewer than u prefix transactions (UFNEXT_STATS=1 only): a
// near-universal hub, like Ethereum's fee recipient, says nothing about
// whether the block is dense once the hubs are out.
func TestHubShareProbe(t *testing.T) {
	if os.Getenv("UFNEXT_STATS") == "" {
		t.Skip("set UFNEXT_STATS=1")
	}
	caps := []int{65, 48, 32, 16}
	for _, tf := range traceFiles {
		blocks, err := loadTrace(tf.path, tf.blocks, tf.density)
		if err != nil {
			continue
		}
		bands := map[string][][]float64{}
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
			shares := make([]float64, len(caps))
			for i, u := range caps {
				w := 0
				for _, tx := range p {
					for _, o := range tx.WriteSet {
						if c := counts[o]; c >= 3 && c < u {
							w++
							break
						}
					}
				}
				shares[i] = float64(w) / 64
			}
			d := density(b)
			band := "<1%"
			switch {
			case d >= 0.1:
				band = ">=10%"
			case d >= 0.03:
				band = "3-10%"
			case d >= 0.01:
				band = "1-3%"
			}
			bands[band] = append(bands[band], shares)
		}
		for _, band := range []string{"<1%", "1-3%", "3-10%", ">=10%"} {
			rows := bands[band]
			if len(rows) == 0 {
				continue
			}
			line := fmt.Sprintf("%-13s %-6s blocks %3d |", tf.name, band, len(rows))
			for i, u := range caps {
				lo, hi, over := 1.0, 0.0, 0
				for _, r := range rows {
					lo, hi = min(lo, r[i]), max(hi, r[i])
					if r[i] >= 0.5 {
						over++
					}
				}
				line += fmt.Sprintf(" u<%d: %.2f-%.2f (>=0.5: %d)", u, lo, hi, over)
			}
			t.Log(line)
		}
	}
}
