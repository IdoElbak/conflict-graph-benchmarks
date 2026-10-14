//go:build ignore

package ufnext_test

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// TestPrefixGiantProbe prints, per trace and density band, the largest
// non-hub set of the 64-transaction prefix (hubs: 3 prefix writes; writes
// union with the object's first writer, reads with the first writer of
// objects the prefix wrote) as a share of the prefix, for blocks whose
// hub-writer share is at least 0.5, the ones the share detector sends to the
// fallback (UFNEXT_STATS=1 only).
func TestPrefixGiantProbe(t *testing.T) {
	if os.Getenv("UFNEXT_STATS") == "" {
		t.Skip("set UFNEXT_STATS=1")
	}
	for _, tf := range traceFiles {
		blocks, err := loadTrace(tf.path, tf.blocks, tf.density)
		if err != nil {
			continue
		}
		bands := map[string][]float64{}
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
			writers := 0
			for _, tx := range p {
				for _, o := range tx.WriteSet {
					if counts[o] >= 3 {
						writers++
						break
					}
				}
			}
			if writers < 32 {
				continue
			}
			parent := make([]int, 64)
			for i := range parent {
				parent[i] = i
			}
			var find func(int) int
			find = func(x int) int {
				for parent[x] != x {
					x = parent[x]
				}
				return x
			}
			first := map[uint32]int{}
			for i, tx := range p {
				for _, o := range tx.WriteSet {
					if counts[o] >= 3 {
						continue
					}
					if f, ok := first[o]; ok {
						parent[find(i)] = find(f)
					} else {
						first[o] = i
					}
				}
			}
			for i, tx := range p {
				for _, o := range tx.ReadSet {
					if f, ok := first[o]; ok && counts[o] < 3 {
						parent[find(i)] = find(f)
					}
				}
			}
			size := map[int]int{}
			largest := 0
			for i := range 64 {
				r := find(i)
				size[r]++
				largest = max(largest, size[r])
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
			hubs := 0
			for _, c := range counts {
				if c >= 3 {
					hubs++
				}
			}
			_ = largest
			bands[band] = append(bands[band], float64(hubs))
		}
		for _, band := range []string{"<1%", "1-3%", "3-10%", ">=10%"} {
			v := bands[band]
			if len(v) == 0 {
				continue
			}
			sort.Float64s(v)
			t.Log(fmt.Sprintf("%-13s %-6s gated %3d | prefix hubs min %.0f q10 %.0f med %.0f q90 %.0f max %.0f", tf.name, band, len(v), v[0], v[len(v)/10], v[len(v)/2], v[9*len(v)/10], v[len(v)-1]))
		}
	}
}
