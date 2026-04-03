package concurrency

import (
	"runtime"
	"sync"
)

// Task is a function type that represents a single unit of work.
// Any function that takes an int and returns nothing matches this type.
type Task func(jobs <-chan int)

func ParallelRun(count int, task Task) {
	numWorkers := runtime.GOMAXPROCS(0) // possible improvement by *2 currentProcs
	var waitGroup sync.WaitGroup

	jobQueue := make(chan int, count)

	for index := range count {
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
