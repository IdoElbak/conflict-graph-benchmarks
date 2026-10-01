package concurrency

import (
	"runtime"
	"sync/atomic"
	"testing"
)

// TestParallelForCoversEveryUnitOnce checks that every unit is handed out
// exactly once and that worker indices stay within [0, GOMAXPROCS).
func TestParallelForCoversEveryUnitOnce(t *testing.T) {
	for _, total := range []int{0, 1, 5, 64, 1000, 1027} {
		for _, chunk := range []int{1, 3, 64, 5000} {
			visits := make([]atomic.Int32, total)
			ParallelFor(total, chunk, func(worker, lo, hi int) {
				if worker < 0 || worker >= runtime.GOMAXPROCS(0) {
					t.Errorf("worker index %d out of range", worker)
				}
				for i := lo; i < hi; i++ {
					visits[i].Add(1)
				}
			})
			for i := range visits {
				if got := visits[i].Load(); got != 1 {
					t.Fatalf("total %d chunk %d: unit %d visited %d times", total, chunk, i, got)
				}
			}
		}
	}
}
