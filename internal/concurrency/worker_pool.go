package concurrency

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// ParallelFor runs body over the units [0, total), split into chunks of
// chunkSize units that GOMAXPROCS workers claim through one atomic counter:
// coordination costs one atomic add per chunk, not a channel operation per
// unit, and nothing is allocated per unit. The calling goroutine is worker 0,
// so only the other workers are spawned. body receives its worker index, in
// [0, GOMAXPROCS), for private scratch, and the half-open chunk [lo, hi).
// Results must not depend on which worker runs a chunk.
func ParallelFor(total, chunkSize int, body func(worker, lo, hi int)) {
	numWorkers := min(runtime.GOMAXPROCS(0), (total+chunkSize-1)/chunkSize)
	var next atomic.Int64
	work := func(worker int) {
		for {
			lo := int(next.Add(int64(chunkSize))) - chunkSize
			if lo >= total {
				return
			}
			body(worker, lo, min(lo+chunkSize, total))
		}
	}

	var waitGroup sync.WaitGroup
	for worker := 1; worker < numWorkers; worker++ {
		waitGroup.Go(func() { work(worker) })
	}
	work(0)
	waitGroup.Wait()
}
