//go:build unix

package main

import (
	"os"
	"syscall"
)

// processCPU reports the CPU time and peak resident set the kernel charged to
// a finished process, from the rusage wait4 already returned - so it costs
// nothing extra and, unlike an in-process clock, cannot differ between the
// two language runtimes by construction.
//
// It is the third quantity the ReFaaS microbenchmark reports alongside energy
// and runtime, and it is what explains a ratio rather than merely stating it:
// wall clock alone cannot separate "Go does less work" from "Go waits less".
func processCPU(ps *os.ProcessState) (cpuSeconds float64, maxRSSBytes int64, ok bool) {
	if ps == nil {
		return 0, 0, false
	}
	ru, isRusage := ps.SysUsage().(*syscall.Rusage)
	if !isRusage {
		return 0, 0, false
	}
	cpu := timevalSeconds(ru.Utime) + timevalSeconds(ru.Stime)
	// ru_maxrss is kilobytes on Linux, bytes on the BSDs. The evaluation host
	// is Linux and this figure is reported, never differenced, so the KB
	// assumption is stated here rather than guessed at the call site.
	return cpu, int64(ru.Maxrss) * 1024, true
}

func timevalSeconds(tv syscall.Timeval) float64 {
	return float64(tv.Sec) + float64(tv.Usec)/1e6
}
