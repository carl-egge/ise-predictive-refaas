//go:build !unix

package main

import "os"

// rusage is a POSIX concept. Measurement needs RAPL or perf anyway, both of
// which are Linux, so elsewhere the CPU columns are simply reported as
// unavailable rather than filled with a substitute.
func processCPU(*os.ProcessState) (float64, int64, bool) { return 0, 0, false }
