package main

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunBudgetScalesWithInvocations(t *testing.T) {
	one := runBudget(1)
	many := runBudget(10000)
	if many <= one {
		t.Errorf("the budget must grow with N so [H6]'s escalation cannot trip it: %s vs %s", one, many)
	}
	if got := runBudget(1 << 30); got != timeoutCap {
		t.Errorf("budget must be capped at %s, got %s", timeoutCap, got)
	}
	if runBudget(0) != runBudget(1) {
		t.Error("a non-positive invocation count must fall back to the N=1 budget")
	}
	// Both sides must get the same budget for the same N, or the timeout
	// biases the comparison toward whichever language is cut off later.
	if runBudget(200) != runBudget(200) {
		t.Error("the budget must be a pure function of N")
	}
}

// The case this exists for: a subprocess that never exits must be killed and
// reported, not waited on forever.
func TestRunWithTimeoutKillsAHangingProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	cmd := exec.Command("sh", "-c", "sleep 300")
	start := time.Now()
	err := runWithTimeout(cmd, 300*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a hanging process must produce an error")
	}
	if !errors.Is(err, errRunTimeout) {
		t.Errorf("the error must be identifiable as a timeout, got %v", err)
	}
	if elapsed > 20*time.Second {
		t.Errorf("runWithTimeout blocked for %s; it must return promptly after the kill", elapsed)
	}
}

// A process that outlives its parent by holding the pipes is exactly why the
// whole group is signalled rather than just the direct child.
func TestRunWithTimeoutKillsTheProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	// The shell spawns a child and exits the foreground; without a group kill
	// the grandchild keeps the pipes open and Wait hangs.
	cmd := exec.Command("sh", "-c", "sleep 300 & wait")
	start := time.Now()
	err := runWithTimeout(cmd, 300*time.Millisecond)
	if !errors.Is(err, errRunTimeout) {
		t.Errorf("expected a timeout, got %v", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("group kill did not release the pipes: took %s", d)
	}
}

func TestRunWithTimeoutPassesThroughNormalCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	if err := runWithTimeout(exec.Command("sh", "-c", "exit 0"), 30*time.Second); err != nil {
		t.Errorf("a fast success must not be disturbed: %v", err)
	}
	err := runWithTimeout(exec.Command("sh", "-c", "exit 3"), 30*time.Second)
	if err == nil {
		t.Fatal("a non-zero exit must still surface")
	}
	if errors.Is(err, errRunTimeout) {
		t.Error("an ordinary failure must not be mislabelled as a timeout")
	}
}

func TestSkipReasonLabelsTimeoutsDistinctly(t *testing.T) {
	got := skipReason("go side", errRunTimeout)
	if !strings.HasPrefix(got, "TIMEOUT:") {
		t.Errorf("a timeout must be labelled distinctly, got %q", got)
	}
	got = skipReason("go side", errors.New("exit status 2"))
	if strings.Contains(got, "TIMEOUT") {
		t.Errorf("an ordinary failure must not be labelled TIMEOUT, got %q", got)
	}
	if !strings.Contains(got, "not runnable") {
		t.Errorf("the ordinary wording must be preserved, got %q", got)
	}
}
