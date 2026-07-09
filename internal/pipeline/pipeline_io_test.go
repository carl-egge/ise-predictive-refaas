package pipeline

import (
	"strings"
	"testing"
	"time"
)

// TestCompilePipelineRejectsUnknownReferences guards against the resolution
// loop spinning forever when a task references a nonexistent id - which,
// reached via /reconfigure, deadlocked the whole service.
func TestCompilePipelineRejectsUnknownReferences(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := compilePipeline(PipelineFile{
			Tasks: []ConversionTaskStub{
				{ID: "root", Task: "noop", Next: []string{"missing-task"}},
			},
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error for an unknown next reference")
		}
		if !strings.Contains(err.Error(), "root") {
			t.Errorf("error should name the unresolved task, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("compilePipeline did not terminate on an unknown reference")
	}
}

// TestCompilePipelineRejectsCycles verifies cyclic next references terminate
// with an error instead of looping forever.
func TestCompilePipelineRejectsCycles(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := compilePipeline(PipelineFile{
			Tasks: []ConversionTaskStub{
				{ID: "root", Task: "noop", Next: []string{"a"}},
				{ID: "a", Task: "noop", Next: []string{"root"}},
			},
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error for cyclic references")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("compilePipeline did not terminate on a cycle")
	}
}

// TestCompilePipelineDefaultsMaxRetryCount verifies a task without an
// explicit maxRetryCount gets one execution instead of silently never
// running (the execute loop condition is RetryCount < MaxRetryCount).
func TestCompilePipelineDefaultsMaxRetryCount(t *testing.T) {
	p, err := compilePipeline(PipelineFile{
		Tasks: []ConversionTaskStub{
			{ID: "root", Task: "noop"},
		},
	})
	if err != nil {
		t.Fatalf("compilePipeline: %v", err)
	}
	if p.FirstTask.MaxRetryCount != 1 {
		t.Errorf("MaxRetryCount = %d, want 1 (one execution)", p.FirstTask.MaxRetryCount)
	}
}
