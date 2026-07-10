package translator

import (
	"bytes"
	"context"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
)

// fakePrepareClient is a minimal llmconnector.Client that records the
// taskParams it receives on each Prepare call, so tests can assert what an
// LLMConverter actually sends the connector without a real backend.
type fakePrepareClient struct {
	prepared []map[string]interface{}
}

func (f *fakePrepareClient) ClientName() string { return "fake" }

func (f *fakePrepareClient) Configure(map[string]interface{}) error { return nil }

func (f *fakePrepareClient) Prepare(taskParams map[string]interface{}) error {
	snap := make(map[string]interface{}, len(taskParams))
	for k, v := range taskParams {
		snap[k] = v
	}
	f.prepared = append(f.prepared, snap)
	return nil
}

func (f *fakePrepareClient) InvokeLLM(context.Context, bytes.Buffer) (string, domain.Metrics, error) {
	return `{"main.go": "package main"}`, domain.Metrics{}, nil
}

// TestLLMConverterRetryTemperature guards [E3]: a task with retry_temperature
// configured must only override "temperature" in the params handed to
// Prepare on attempt >1 (ConversionRequest.CurrentAttempt), and must never
// mutate its own long-lived taskParams map - doing so would leak the bumped
// value into a later, unrelated first attempt.
func TestLLMConverterRetryTemperature(t *testing.T) {
	client := &fakePrepareClient{}
	runner := pipeline.NewRunner(context.Background(), nil, client)

	conv := NewLLMConverter(map[string]interface{}{
		"prompt":            "irrelevant",
		"temperature":       0.1,
		"retry_temperature": 0.9,
	})

	req := &domain.ConversionRequest{CurrentAttempt: 1}
	if err := conv.Apply(runner, req); err != nil {
		t.Fatalf("Apply (attempt 1) failed: %v", err)
	}
	req.CurrentAttempt = 2
	if err := conv.Apply(runner, req); err != nil {
		t.Fatalf("Apply (attempt 2) failed: %v", err)
	}

	if len(client.prepared) != 2 {
		t.Fatalf("Prepare called %d times, want 2", len(client.prepared))
	}
	if got := client.prepared[0]["temperature"]; got != 0.1 {
		t.Errorf("attempt 1 temperature = %v, want base 0.1", got)
	}
	if got := client.prepared[1]["temperature"]; got != 0.9 {
		t.Errorf("attempt 2 temperature = %v, want bumped 0.9", got)
	}

	llmConv, ok := conv.(*LLMConverter)
	if !ok {
		t.Fatalf("NewLLMConverter returned %T, want *LLMConverter", conv)
	}
	if got := llmConv.taskParams["temperature"]; got != 0.1 {
		t.Errorf("converter's own taskParams temperature = %v, want unchanged base 0.1 (the retry bump must not leak into it)", got)
	}
}

// TestLLMConverterNoRetryTemperatureConfigured verifies a task without
// retry_temperature sees the same temperature on every attempt - the
// override path must be a true no-op when unconfigured.
func TestLLMConverterNoRetryTemperatureConfigured(t *testing.T) {
	client := &fakePrepareClient{}
	runner := pipeline.NewRunner(context.Background(), nil, client)

	conv := NewLLMConverter(map[string]interface{}{
		"prompt":      "irrelevant",
		"temperature": 0.1,
	})

	req := &domain.ConversionRequest{CurrentAttempt: 2}
	if err := conv.Apply(runner, req); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if len(client.prepared) != 1 {
		t.Fatalf("Prepare called %d times, want 1", len(client.prepared))
	}
	if got := client.prepared[0]["temperature"]; got != 0.1 {
		t.Errorf("temperature = %v, want unchanged base 0.1 with no retry_temperature configured", got)
	}
}
