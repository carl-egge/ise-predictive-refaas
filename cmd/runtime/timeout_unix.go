//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so the whole tree
// can be signalled at once. Without it, killing the direct child can leave a
// grandchild holding the stdout/stderr pipes, and Wait blocks on them.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs the group created by setProcessGroup, falling back
// to the single process if the group is not available.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
