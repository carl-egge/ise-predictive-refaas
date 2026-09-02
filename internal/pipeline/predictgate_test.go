package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// writeModel puts a one-feature logistic model on disk. coefficient > 0 means
// a larger `cc` scores higher, which makes the fixtures below readable.
func writeModel(t *testing.T, threshold float64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.json")
	body := map[string]any{
		"model":                  "logistic_regression",
		"feature_schema_version": 1,
		"features":               []string{"cc"},
		"mean":                   []float64{0},
		"scale":                  []float64{1},
		"coefficients":           []float64{1},
		"intercept":              0.0,
		"threshold":              threshold,
		"provenance":             map[string]any{"id": "test-model"},
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// gateRequest builds a request carrying a feature vector, as pyScan would.
func gateRequest(cc float64) *domain.ConversionRequest {
	req := &domain.ConversionRequest{
		Metadata: map[string]string{},
		Metrics:  &domain.Metrics{FunctionID: "f-test"},
	}
	req.Metrics.RecordFeatures(1, []string{"cc"}, []float64{cc})
	return req
}

func gateRunner(cfg PredictConfig) *Runner {
	return &Runner{predict: cfg}
}

// TestPredictGateDisabledIsANoOp is the property every existing baseline in
// section H depends on: a pipeline that names the stage but has not enabled it
// must behave exactly as if the stage were absent.
func TestPredictGateDisabledIsANoOp(t *testing.T) {
	gate := NewPredictGateConverter(nil)
	req := gateRequest(0)
	// No model configured at all: a disabled gate must not even look for one.
	if err := gate.Apply(gateRunner(PredictConfig{}), req); err != nil {
		t.Fatalf("a disabled gate must pass through, got %v", err)
	}
	if req.Metrics.Prediction != nil {
		t.Fatalf("a disabled gate must not record a score, got %+v", req.Metrics.Prediction)
	}
}

func TestPredictGateEnforcesBelowThreshold(t *testing.T) {
	cfg := PredictConfig{Enabled: true, Enforce: true, ModelPath: writeModel(t, 0.9)}
	gate := NewPredictGateConverter(nil)
	req := gateRequest(0) // sigmoid(0) = 0.5, below 0.9

	err := gate.Apply(gateRunner(cfg), req)
	if err == nil {
		t.Fatal("expected the gate to decline this candidate")
	}
	if !domain.IsPredictionSkip(err) {
		t.Fatalf("expected a domain.PredictionSkip, got %T: %v", err, err)
	}
	rec := req.Metrics.Prediction
	if rec == nil {
		t.Fatal("a skipped job must still carry its score")
	}
	if !rec.Skipped || rec.Translate {
		t.Fatalf("expected skipped=true translate=false, got %+v", rec)
	}
	if rec.Model != "test-model" {
		t.Fatalf("expected the model id on the record, got %q", rec.Model)
	}
}

// TestPredictGateScoresWithoutEnforcing covers the deployment [I10] treats as
// the default one: score every job, record it, change nothing. That is what
// keeps "would this have succeeded?" answerable once a gate is deployed.
func TestPredictGateScoresWithoutEnforcing(t *testing.T) {
	cfg := PredictConfig{Enabled: true, Enforce: false, ModelPath: writeModel(t, 0.9)}
	gate := NewPredictGateConverter(nil)
	req := gateRequest(0)

	if err := gate.Apply(gateRunner(cfg), req); err != nil {
		t.Fatalf("a non-enforcing gate must not stop the job, got %v", err)
	}
	rec := req.Metrics.Prediction
	if rec == nil {
		t.Fatal("a non-enforcing gate must still record its score")
	}
	if rec.Skipped {
		t.Fatal("a non-enforcing gate must not mark the job skipped")
	}
	if rec.Translate {
		t.Fatalf("the recorded decision should be the model's (false), got %+v", rec)
	}
}

func TestPredictGatePassesAboveThreshold(t *testing.T) {
	cfg := PredictConfig{Enabled: true, Enforce: true, ModelPath: writeModel(t, 0.5)}
	gate := NewPredictGateConverter(nil)
	req := gateRequest(4) // sigmoid(4) ~ 0.982

	if err := gate.Apply(gateRunner(cfg), req); err != nil {
		t.Fatalf("expected the gate to allow this candidate, got %v", err)
	}
	rec := req.Metrics.Prediction
	if rec == nil || !rec.Translate || rec.Skipped {
		t.Fatalf("expected translate=true skipped=false, got %+v", rec)
	}
}

// TestPredictGateFailsClosedWithoutFeatures guards the ordering requirement.
// A gate that passed a job through because pyScan had not run would report
// "translated everything" while claiming a gate was active.
func TestPredictGateFailsClosedWithoutFeatures(t *testing.T) {
	cfg := PredictConfig{Enabled: true, Enforce: true, ModelPath: writeModel(t, 0.5)}
	gate := NewPredictGateConverter(nil)
	req := &domain.ConversionRequest{Metrics: &domain.Metrics{FunctionID: "f-test"}}

	err := gate.Apply(gateRunner(cfg), req)
	if err == nil {
		t.Fatal("an enabled gate with no feature vector must fail, not pass through")
	}
	if domain.IsPredictionSkip(err) {
		t.Fatal("a missing feature vector is a configuration error, not a skip decision")
	}
	if !strings.Contains(err.Error(), "pyScan") {
		t.Fatalf("the error should point at the ordering requirement, got %v", err)
	}
}

func TestPredictGateFailsClosedWithoutModel(t *testing.T) {
	gate := NewPredictGateConverter(nil)
	err := gate.Apply(gateRunner(PredictConfig{Enabled: true}), gateRequest(0))
	if err == nil {
		t.Fatal("an enabled gate with no model must fail, not pass through")
	}
	if !strings.Contains(err.Error(), "no model configured") {
		t.Fatalf("the error should say the model is missing, got %v", err)
	}
}

func TestPredictGateRefusesSchemaMismatch(t *testing.T) {
	cfg := PredictConfig{Enabled: true, Enforce: true, ModelPath: writeModel(t, 0.5)}
	gate := NewPredictGateConverter(nil)
	req := &domain.ConversionRequest{Metrics: &domain.Metrics{FunctionID: "f-test"}}
	req.Metrics.RecordFeatures(99, []string{"cc"}, []float64{4})

	err := gate.Apply(gateRunner(cfg), req)
	if err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("expected a schema-version refusal, got %v", err)
	}
}

// TestPredictGateThresholdOverride checks that a configured operating point
// replaces the model's while the recorded score stays the model's own.
func TestPredictGateThresholdOverride(t *testing.T) {
	override := 0.99
	cfg := PredictConfig{
		Enabled:   true,
		Enforce:   true,
		ModelPath: writeModel(t, 0.5),
		Threshold: &override,
	}
	req := gateRequest(4) // ~0.982: above the model's 0.5, below the override
	err := NewPredictGateConverter(nil).Apply(gateRunner(cfg), req)
	if !domain.IsPredictionSkip(err) {
		t.Fatalf("the override should have declined this candidate, got %v", err)
	}
	rec := req.Metrics.Prediction
	if rec.Threshold != override {
		t.Fatalf("expected the override threshold on the record, got %v", rec.Threshold)
	}
	if rec.Score < 0.98 || rec.Score > 0.99 {
		t.Fatalf("the recorded score should be the model's own, got %v", rec.Score)
	}
}

// TestPredictGateTaskArgsOverrideRunner covers the per-task knobs.
func TestPredictGateTaskArgsOverrideRunner(t *testing.T) {
	runnerCfg := PredictConfig{Enabled: true, Enforce: true, ModelPath: writeModel(t, 0.9)}
	gate := NewPredictGateConverter(map[string]interface{}{"enforce": false})
	req := gateRequest(0)
	if err := gate.Apply(gateRunner(runnerCfg), req); err != nil {
		t.Fatalf("task_args.enforce=false should override the Runner, got %v", err)
	}
	if req.Metrics.Prediction.Skipped {
		t.Fatal("the task-level override should have prevented the skip")
	}
}

// TestPredictGateIsRegistered checks the stage resolves by name, since that is
// how a pipeline YAML/JSON reaches it.
func TestPredictGateIsRegistered(t *testing.T) {
	conv, err := MakeConverter("predictGate", nil)
	if err != nil {
		t.Fatalf("predictGate is not registered: %v", err)
	}
	if _, ok := conv.(*PredictGateConverter); !ok {
		t.Fatalf("predictGate resolved to %T", conv)
	}
}
