//go:build !unix

package main

import "os/exec"

// Process groups are a POSIX concept; elsewhere the single process is all
// that can be signalled. Measurement needs RAPL or perf anyway, both Linux.
func setProcessGroup(*exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
