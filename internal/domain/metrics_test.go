package domain

import "testing"

// TestRecordLLMCallRecordsModel guards [H3]: the model that produced a
// stage's tokens must be recorded per stage, because energy per token is
// model-specific and a pipeline may set model_name per task.
func TestRecordLLMCallRecordsModel(t *testing.T) {
	m := &Metrics{}

	m.RecordLLMCall("convert", Metrics{
		Model:                      "devstral-2-123b",
		ConversionPromptTokenCount: 1000,
		ConversionEvalTokenCount:   200,
	})
	m.RecordLLMCall("convert", Metrics{
		Model:                      "devstral-2-123b",
		ConversionPromptTokenCount: 500,
		ConversionEvalTokenCount:   100,
	})

	tm := m.PerTask["convert"]
	if tm == nil {
		t.Fatal("no per-task metrics recorded")
	}
	if tm.Model != "devstral-2-123b" {
		t.Errorf("Model = %q, want the single model that served the stage", tm.Model)
	}
	if tm.LLMCalls != 2 || tm.PromptTokens != 1500 || tm.EvalTokens != 300 {
		t.Errorf("token aggregation broke: %+v", tm)
	}
}

// TestRecordLLMCallSeparatesModelsPerStage: a pipeline that runs a cheap
// model for one stage and a large one for another must stay costable.
func TestRecordLLMCallSeparatesModelsPerStage(t *testing.T) {
	m := &Metrics{}
	m.RecordLLMCall("root", Metrics{Model: "qwen2.5-coder:3b", ConversionPromptTokenCount: 10})
	m.RecordLLMCall("convert", Metrics{Model: "devstral-2-123b", ConversionPromptTokenCount: 20})

	if got := m.PerTask["root"].Model; got != "qwen2.5-coder:3b" {
		t.Errorf("root model = %q", got)
	}
	if got := m.PerTask["convert"].Model; got != "devstral-2-123b" {
		t.Errorf("convert model = %q", got)
	}
}

// TestRecordLLMCallKeepsConflictingModelsVisible: a stage served by two
// different models cannot be costed with one coefficient pair, so the second
// name must not be silently dropped.
func TestRecordLLMCallKeepsConflictingModelsVisible(t *testing.T) {
	m := &Metrics{}
	m.RecordLLMCall("convert", Metrics{Model: "model-a"})
	m.RecordLLMCall("convert", Metrics{Model: "model-b"})
	m.RecordLLMCall("convert", Metrics{Model: "model-a"}) // already known

	if got := m.PerTask["convert"].Model; got != "model-a,model-b" {
		t.Errorf("Model = %q, want both names recorded once each", got)
	}
}

// TestRecordLLMCallToleratesMissingModel: a connector that reports no model
// must not blank out one already recorded, nor crash.
func TestRecordLLMCallToleratesMissingModel(t *testing.T) {
	m := &Metrics{}
	m.RecordLLMCall("convert", Metrics{Model: "model-a"})
	m.RecordLLMCall("convert", Metrics{})

	if got := m.PerTask["convert"].Model; got != "model-a" {
		t.Errorf("Model = %q, want the known name kept", got)
	}
}

// TestAddMetricDoesNotAggregateModel: the request-level Metrics spans every
// stage, so a single model name there would be misleading - per-stage models
// live in PerTask.
func TestAddMetricDoesNotAggregateModel(t *testing.T) {
	m := &Metrics{}
	m.AddMetric(Metrics{Model: "devstral-2-123b", ConversionPromptTokenCount: 100})

	if m.Model != "" {
		t.Errorf("request-level Model = %q, want it left empty", m.Model)
	}
	if m.ConversionPromptTokenCount != 100 {
		t.Errorf("token aggregation broke: %+v", m)
	}
}
