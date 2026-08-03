package translator

import (
	"bytes"
	"context"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
)

// stubClient is a test LLM client that returns a canned response and the
// per-call metrics a real connector would report, including the model.
type stubClient struct {
	response string
	model    string
	prompt   int
	eval     int
}

func (s *stubClient) ClientName() string                     { return "stub" }
func (s *stubClient) Configure(map[string]interface{}) error { return nil }
func (s *stubClient) Prepare(map[string]interface{}) error   { return nil }
func (s *stubClient) InvokeLLM(context.Context, bytes.Buffer) (string, domain.Metrics, error) {
	return s.response, domain.Metrics{
		Model:                      s.model,
		ConversionPromptTokenCount: s.prompt,
		ConversionEvalTokenCount:   s.eval,
	}, nil
}

// TestLLMCallAttributedToTaskAndModel is the end-to-end half of [H3]/[B5]:
// a stage's token spend and the model that produced it must land on that
// stage's TaskMetrics, which is what the energy analysis sums per stage.
func TestLLMCallAttributedToTaskAndModel(t *testing.T) {
	client := &stubClient{
		response: `{"main.go": "package main"}`,
		model:    "devstral-2-123b",
		prompt:   1200,
		eval:     340,
	}
	runner := pipeline.NewRunner(context.Background(), nil, client)

	conv, ok := NewCodeConverter(map[string]interface{}{
		"reader":     "go",
		"model_name": "devstral-2-123b",
	}).(*LLMConverter)
	if !ok {
		t.Fatal("NewCodeConverter did not return an LLMConverter")
	}

	req := &domain.ConversionRequest{
		SourcePackage: &domain.DeploymentPackage{
			RootFile:  "def handler(event, context):\n    return {}",
			Suffix:    "py",
			TestFiles: map[string]string{"test/t1.json": `{"input":"{}","output":"{}"}`},
		},
		Metrics:     &domain.Metrics{TestCases: map[string]bool{}},
		CurrentTask: "convert",
	}
	req.WorkingPackage = req.SourcePackage.Copy()

	if err := conv.Apply(runner, req); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	tm := req.Metrics.PerTask["convert"]
	if tm == nil {
		t.Fatal("the call was not attributed to the running task")
	}
	if tm.Model != "devstral-2-123b" {
		t.Errorf("PerTask model = %q, want the model the connector reported", tm.Model)
	}
	if tm.LLMCalls != 1 || tm.PromptTokens != 1200 || tm.EvalTokens != 340 {
		t.Errorf("token attribution wrong: %+v", tm)
	}
	// the request-level totals still aggregate across stages
	if req.Metrics.ConversionPromptTokenCount != 1200 {
		t.Errorf("request-level tokens = %d, want 1200", req.Metrics.ConversionPromptTokenCount)
	}
}

// TestLLMCallAttributionUsesConnectorModel: when the task params name no
// model (the Gemini case, which reads GEMINI_MODEL instead), the model the
// connector reports is still what gets recorded.
func TestLLMCallAttributionUsesConnectorModel(t *testing.T) {
	client := &stubClient{
		response: `{"main.go": "package main"}`,
		model:    "gemini-2.5-flash",
		prompt:   10,
	}
	runner := pipeline.NewRunner(context.Background(), nil, client)

	conv := NewCodeConverter(map[string]interface{}{"reader": "go"}).(*LLMConverter)
	req := &domain.ConversionRequest{
		SourcePackage: &domain.DeploymentPackage{RootFile: "def handler(): pass", Suffix: "py"},
		Metrics:       &domain.Metrics{TestCases: map[string]bool{}},
		CurrentTask:   "convert",
	}
	req.WorkingPackage = req.SourcePackage.Copy()

	if err := conv.Apply(runner, req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := req.Metrics.PerTask["convert"].Model; got != "gemini-2.5-flash" {
		t.Errorf("PerTask model = %q, want the connector's model even without model_name", got)
	}
}
