package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// TestOptionalTaskFailureDoesNotFailTheJob is the regression test for f62 of
// run 20260831-190900: two ChatAI timeouts in the opening prompt-enrichment
// stage ended the conversion there, with zero build attempts, over a sentence
// the translation would have been fine without.
func TestOptionalTaskFailureDoesNotFailTheJob(t *testing.T) {
	enrich := &stubConverter{err: domain.NewLLMError(errors.New("context deadline exceeded"))}
	translate := &stubConverter{}

	task := &ConversionTask{
		ID:            "summarize",
		Execute:       enrich,
		MaxRetryCount: 2,
		Optional:      true,
		Next:          []*ConversionTask{{ID: "convert", Execute: translate, MaxRetryCount: 1}},
	}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	req := &domain.ConversionRequest{}
	if err := p.Execute(runner, req); err != nil {
		t.Fatalf("an optional stage failing must not fail the conversion: %v", err)
	}
	// The retries still happen - degrading is the last resort, not the first.
	if enrich.calls != 2 {
		t.Errorf("optional task ran %d times, want 2 (its retry budget is still spent)", enrich.calls)
	}
	if translate.calls != 1 {
		t.Errorf("downstream task ran %d times, want 1", translate.calls)
	}
	// The failure is still recorded: a degraded job must be distinguishable
	// from a clean one in the run log.
	if len(req.Errors()) == 0 {
		t.Error("the optional failure was not recorded; a degraded run would look identical to a clean one")
	}
}

// TestNonOptionalTaskStillFailsTheJob pins the default: Optional is opt-in, so
// every existing pipeline behaves exactly as before.
func TestNonOptionalTaskStillFailsTheJob(t *testing.T) {
	failing := &stubConverter{err: errors.New("boom")}
	downstream := &stubConverter{}

	task := &ConversionTask{
		ID:            "summarize",
		Execute:       failing,
		MaxRetryCount: 2,
		Next:          []*ConversionTask{{ID: "convert", Execute: downstream, MaxRetryCount: 1}},
	}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	if err := p.Execute(runner, &domain.ConversionRequest{}); err == nil {
		t.Fatal("a task without Optional must still fail the conversion")
	}
	if downstream.calls != 0 {
		t.Errorf("downstream ran %d times after an unrecoverable failure, want 0", downstream.calls)
	}
}

// TestOptionalTaskSkipsItsValidation covers the one interaction that would
// otherwise reintroduce the failure through the back door: validating the
// output of a stage that produced none.
func TestOptionalTaskSkipsItsValidation(t *testing.T) {
	enrich := &stubConverter{err: errors.New("no response")}
	validation := &stubConverter{err: errors.New("nothing to validate")}
	downstream := &stubConverter{}

	task := &ConversionTask{
		ID:            "summarize",
		Execute:       enrich,
		Validation:    validation,
		MaxRetryCount: 1,
		Optional:      true,
		Next:          []*ConversionTask{{ID: "convert", Execute: downstream, MaxRetryCount: 1}},
	}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	if err := p.Execute(runner, &domain.ConversionRequest{}); err != nil {
		t.Fatalf("validation of a degraded optional stage must not fail the job: %v", err)
	}
	if validation.calls != 0 {
		t.Errorf("validation ran %d times on a stage that produced nothing, want 0", validation.calls)
	}
	if downstream.calls != 1 {
		t.Errorf("downstream ran %d times, want 1", downstream.calls)
	}
}

// TestOptionalTaskStillHonoursCancellation is the limit of the mechanism:
// "optional" must mean "its failure is tolerable", never "this job cannot be
// stopped". Cancellation returns from inside the retry loop, before the
// degrade path is reached.
func TestOptionalTaskStillHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	enrich := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		cancel()
		return errors.New("interrupted")
	}}
	downstream := &stubConverter{}

	task := &ConversionTask{
		ID:            "summarize",
		Execute:       enrich,
		MaxRetryCount: 3,
		Optional:      true,
		Next:          []*ConversionTask{{ID: "convert", Execute: downstream, MaxRetryCount: 1}},
	}
	p := NewPipeline(task)
	runner := NewRunner(ctx, p, nil)

	if err := p.Execute(runner, &domain.ConversionRequest{}); err == nil {
		t.Fatal("a cancelled job must abort even when the failing stage is optional")
	}
	if downstream.calls != 0 {
		t.Errorf("downstream ran %d times after cancellation, want 0", downstream.calls)
	}
}

// TestOptionalSurvivesPipelineCompilation checks the field actually reaches
// the executable graph: it is set in JSON/YAML, and a silent drop between the
// stub and the task would turn the fault-safety off without any visible sign.
func TestOptionalSurvivesPipelineCompilation(t *testing.T) {
	file := PipelineFile{Tasks: []ConversionTaskStub{
		{ID: "root", Task: "canCompile", MaxRetryCount: 1, Optional: true},
	}}
	compiled, err := compilePipeline(file)
	if err != nil {
		t.Fatalf("compilePipeline: %v", err)
	}
	if !compiled.FirstTask.Optional {
		t.Error("optional was lost between the task stub and the compiled task")
	}
}
