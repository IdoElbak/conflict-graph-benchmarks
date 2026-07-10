package concurrency

import (
	"runtime"
	"sync"
)

// Task defines the signature for a worker function that consumes transaction indices from a channel
type Task func(jobs <-chan int)

// ParallelRun orchestrates a fixed-size worker pool to execute a task across all available logical cores
func ParallelRun(totalTasks int, task Task) {
	numWorkers := runtime.GOMAXPROCS(0)
	var waitGroup sync.WaitGroup

	jobQueue := make(chan int, totalTasks)

	for index := range totalTasks {
		jobQueue <- index
	}
	close(jobQueue)

	for i := 0; i < numWorkers; i++ {
		waitGroup.Go(func() {
			task(jobQueue)
		})
	}
	waitGroup.Wait()
}
