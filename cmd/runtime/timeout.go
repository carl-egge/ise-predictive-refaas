package main

import (
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Per-invocation deadlines for the measured subprocesses ([H10]).
//
// Why this exists: nothing bounded the measured invocation, so a translated
// function that does not terminate stalled the whole pass indefinitely. It
// happened twice on the evaluation_set corpus - f52's binary ran 25 minutes at
// ~45% CPU with the run frozen behind it, and f51 later did the same. The pass
// is long, unattended, and the input to every N* in the thesis, so one hang
// costing the whole run is not acceptable.
//
// The budget scales with the invocation count because [H6]'s N escalation
// legitimately makes later rounds far longer than earlier ones; a fixed
// deadline would either be uselessly loose at N=1 or cut off honest work at
// N=100000. Both language sides get the same budget, so the timeout cannot
// bias the comparison by cutting one side off sooner than the other.
const (
	// timeoutBase covers process startup, interpreter import time and, on the
	// Go side, the first-call cost of any AWS SDK client construction.
	timeoutBase = 90 * time.Second
	// timeoutPerInvocation is deliberately generous: the slowest honest
	// function in this corpus runs ~7 ms per invocation, so 50 ms leaves
	// roughly 7x headroom before a real workload is mistaken for a hang.
	timeoutPerInvocation = 50 * time.Millisecond
	// timeoutCap bounds the budget at the top of the escalation ladder.
	timeoutCap = 15 * time.Minute
)

// errRunTimeout marks a subprocess killed for exceeding its budget, so the
// caller can report it as TIMEOUT rather than as a generic "not runnable" -
// a function that builds, deploys and then hangs is a different failure from
// one that never compiled, and [I1]'s labels should be able to tell them apart.
var errRunTimeout = errors.New("timed out")

// runBudget returns the wall-clock budget for one measured run of the given
// invocation count.
func runBudget(invocations int) time.Duration {
	if invocations < 1 {
		invocations = 1
	}
	d := timeoutBase + time.Duration(invocations)*timeoutPerInvocation
	if d > timeoutCap {
		d = timeoutCap
	}
	return d
}

// runWithTimeout runs cmd, killing it if it outlives the budget.
//
// It kills the process *group* rather than the process: a translated function
// that shells out would otherwise leave the child holding the pipes, and Wait
// would block on them even after the direct child died. The group is
// established by setProcessGroup (see timeout_unix.go).
func runWithTimeout(cmd *exec.Cmd, budget time.Duration) error {
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(budget)
	defer timer.Stop()

	select {
	case err := <-done:
		return err
	case <-timer.C:
		killProcessGroup(cmd)
		// Reap, so the goroutine and the process table entry both go away.
		// The kill makes this return promptly; a second bound guards the
		// pathological case where even SIGKILL does not land (uninterruptible
		// sleep in a syscall), which no user-space fix can resolve anyway.
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
		return fmt.Errorf("%w after %s", errRunTimeout, budget)
	}
}
