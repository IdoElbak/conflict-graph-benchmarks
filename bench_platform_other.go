//go:build !windows

package main

import "time"

// clockStart anchors nowNanos to Go's monotonic clock, which is
// nanosecond-resolution on Linux and macOS.
var clockStart = time.Now()

// nowNanos returns monotonic nanoseconds since process start.
func nowNanos() int64 {
	return int64(time.Since(clockStart))
}

// prepareProcess needs no platform tweaks outside Windows.
func prepareProcess() string {
	return "timer time.Now (monotonic), default scheduling"
}
