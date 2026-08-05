package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// documentedHardware is the configuration EVALUATION.md section 3 derives its
// published coefficients from: 4x H100 PCIe, MFU 0.40, 2 kW node.
func documentedHardware() HardwareParams {
	return HardwareParams{
		GPUs:              4,
		PeakFLOPSPerGPU:   756e12,
		MFU:               0.40,
		HBMBandwidth:      2.0e12,
		BandwidthFraction: 0.75,
		NodePowerWatts:    2000,
	}
}

// documentedModel is Devstral 2 123B at BF16.
func documentedModel() ModelConfig {
	return ModelConfig{Parameters: 123e9, BytesPerParameter: 2}
}

func closeTo(t *testing.T, name string, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s = %v, want %v (±%v)", name, got, want, tolerance)
	}
}

// TestDeriveCoefficientsMatchesDocumentedValues checks the implementation
// against the figures published in EVALUATION.md section 3. If this fails,
// either the code or the thesis document is wrong - and they must not drift
// apart, because the document is what the write-up cites.
func TestDeriveCoefficientsMatchesDocumentedValues(t *testing.T) {
	c := DeriveCoefficients(documentedHardware(), documentedModel(), 32)

	// T_prefill = (4 * 756e12 * 0.40) / (2 * 123e9) ~ 4,900 tokens/s
	closeTo(t, "prefill tokens/s", c.PrefillTokensPerSecond, 4900, 50)
	// e_in = 2000 W / 4900 ~ 0.41 J per input token
	closeTo(t, "e_in", c.EIn, 0.41, 0.01)
	// t_step = 246 GB / (4 * 2.0e12 * 0.75) ~ 41 ms
	closeTo(t, "decode step (ms)", c.DecodeStepSeconds*1000, 41, 0.5)
	// e_out at B=32 ~ 2.6 J per output token
	closeTo(t, "e_out", c.EOut, 2.6, 0.05)
}

// TestEOutScalesWithConcurrency reproduces the concurrency table of section 3.
// B is the single largest unknown in the model, so the relationship it enters
// through is worth pinning.
func TestEOutScalesWithConcurrency(t *testing.T) {
	for _, tc := range []struct {
		concurrency float64
		wantEOut    float64
	}{
		{8, 10.3},
		{16, 5.1},
		{32, 2.6},
		{64, 1.3},
		{128, 0.64},
	} {
		c := DeriveCoefficients(documentedHardware(), documentedModel(), tc.concurrency)
		closeTo(t, "e_out at B="+string(rune('0'+int(tc.concurrency/64)))+"x", c.EOut, tc.wantEOut, 0.06)
	}
}

// TestWorkedExample reproduces section 3's worked example end to end: five
// calls averaging 6,000 prompt and 1,500 output tokens come to roughly 33 kJ
// (~9.3 Wh) of facility energy.
func TestWorkedExample(t *testing.T) {
	cfg := testConfig()
	rec := JobRecord{
		Type:       recordTypeJob,
		FunctionID: "f1",
		Metrics: &domain.Metrics{
			PerTask: map[string]*domain.TaskMetrics{
				// one stage standing in for the five calls
				"convert": {LLMCalls: 5, PromptTokens: 30000, EvalTokens: 7500, Model: "devstral"},
			},
		},
	}

	got := Evaluate(cfg, rec)

	closeTo(t, "compute joules", got.ComputeJoules, 31800, 700)
	closeTo(t, "facility joules", got.FacilityJoules, 33000, 800)
	// and the CO2e conversion: 33 kJ = 9.2 Wh at 363 g/kWh
	closeTo(t, "co2e grams", got.CO2eGrams, 3.3, 0.2)
}

func testConfig() *Config {
	cfg := &Config{}
	cfg.Hardware.GPUsPerNode = 4
	cfg.Hardware.PeakFLOPSPerGPU = 756e12
	cfg.Hardware.ModelFLOPUtilization = 0.40
	cfg.Hardware.HBMBandwidthBytesPerSecond = 2.0e12
	cfg.Hardware.AchievedBandwidthFraction = 0.75
	cfg.Hardware.NodePowerWatts = 2000
	cfg.Serving.Concurrency = 32
	cfg.Facility.PUE = 1.05
	cfg.Facility.GridCO2eGramsPerKWh = 363
	cfg.Models = map[string]ModelConfig{
		defaultModelKey: documentedModel(),
		"devstral":      documentedModel(),
	}
	cfg.Analysis.RepairStages = []string{"gollmRecovery"}
	return cfg
}

// TestShippedConfigIsValid guards the constants file itself: a typo there
// would silently change every number in the thesis.
func TestShippedConfigIsValid(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", defaultConfigPath))
	if err != nil {
		t.Fatalf("the shipped energy config must load: %v", err)
	}

	c := DeriveCoefficients(cfg.hardware(), cfg.Models[defaultModelKey], cfg.Serving.Concurrency)
	closeTo(t, "shipped config e_in", c.EIn, 0.41, 0.01)
	closeTo(t, "shipped config e_out", c.EOut, 2.6, 0.05)
	if len(cfg.Sensitivity.Concurrency) == 0 {
		t.Error("the sensitivity sweep needs a concurrency range to produce the section 8 table")
	}
	if len(cfg.Analysis.RepairStages) == 0 {
		t.Error("repair_stages must name the pipeline's recovery tasks, or the repair share is always zero")
	}
}

func TestLoadConfigRejectsIncomplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	// no _default model entry: a run using an unlisted model would be costed
	// as zero energy, the one failure mode this tool must not have
	if err := os.WriteFile(path, []byte(`{"hardware":{"gpus_per_node":4,"peak_flops_per_gpu":1,
		"model_flop_utilization":0.4,"hbm_bandwidth_bytes_per_second":1,
		"achieved_bandwidth_fraction":0.75,"node_power_watts":2000},
		"serving":{"concurrency":32},"facility":{"pue":1.05},"models":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected a config without a _default model to be rejected")
	} else if !strings.Contains(err.Error(), defaultModelKey) {
		t.Errorf("error should name the missing entry, got: %v", err)
	}
}

// TestEvaluateSplitsRepairEnergy: "the repair loop accounts for X% of pipeline
// energy" is a finding, not just a number - it needs the per-stage split.
func TestEvaluateSplitsRepairEnergy(t *testing.T) {
	cfg := testConfig()
	rec := JobRecord{
		Type:       recordTypeJob,
		FunctionID: "f42",
		Metrics: &domain.Metrics{
			Meta: &domain.FunctionMeta{Bucket: "C", AWS: true},
			PerTask: map[string]*domain.TaskMetrics{
				"convert":       {Executions: 1, LLMCalls: 1, PromptTokens: 1000, EvalTokens: 200, Model: "devstral"},
				"gollmRecovery": {Executions: 3, Failures: 2, LLMCalls: 3, PromptTokens: 3000, EvalTokens: 600, Model: "devstral"},
			},
			TestOutcomes: []domain.TestOutcome{
				{Name: "t1", Passed: true, OutputMode: "tolerant"},
				{Name: "t2", OutputMode: "shape", Kind: domain.TestFailureMismatch},
			},
		},
	}

	got := Evaluate(cfg, rec)

	if len(got.Stages) != 2 {
		t.Fatalf("expected both stages costed, got %+v", got.Stages)
	}
	if got.Bucket != "C" || !got.UsesAWS {
		t.Errorf("dataset grouping metadata not carried through: %+v", got)
	}
	// the repair stage burned 3x the tokens of the first pass
	if got.RepairJoules <= got.ComputeJoules/2 {
		t.Errorf("repair share looks wrong: repair %v of total %v", got.RepairJoules, got.ComputeJoules)
	}
	if got.TestsPassed != 1 || got.TestsFailed != 1 || got.ShapeOnlyTests != 1 {
		t.Errorf("outcome summary = %+v", got)
	}
	if got.FailureKinds[domain.TestFailureMismatch] != 1 {
		t.Errorf("failure kinds not counted: %v", got.FailureKinds)
	}
}

// TestEvaluateFlagsUnknownModel: a model missing from the config must be
// costed on the default and *said* to be, never silently.
func TestEvaluateFlagsUnknownModel(t *testing.T) {
	cfg := testConfig()
	rec := JobRecord{Metrics: &domain.Metrics{PerTask: map[string]*domain.TaskMetrics{
		"convert": {PromptTokens: 100, Model: "some-unlisted-model"},
	}}}

	got := Evaluate(cfg, rec)
	if !got.Stages[0].ModelAssumed {
		t.Error("an unlisted model must be marked as costed on an assumption")
	}
	if got.ComputeJoules <= 0 {
		t.Error("an unlisted model must still be costed, not dropped")
	}

	report := Build(cfg, []TranslationEnergy{got}, nil)
	if len(report.AssumedModels) != 1 || report.AssumedModels[0] != "some-unlisted-model" {
		t.Errorf("the report must surface the assumption: %v", report.AssumedModels)
	}
}

func TestReadRunLogs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.jsonl")
	lines := strings.Join([]string{
		`{"type":"run_start","run_id":"r1"}`,
		`{"type":"job","run_id":"r1","job_id":"j1","function_id":"f1","metrics":{"per_task":{"convert":{"prompt_tokens":10}}}}`,
		`{"type":"reconfigure","run_id":"r1"}`,
		`{"type":"job","run_id":"r1","job_id":"j2","function_id":"f2","metrics":{}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := ReadRunLogs([]string{path})
	if err != nil {
		t.Fatalf("ReadRunLogs: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected the two job records, got %d", len(records))
	}
	if records[0].FunctionID != "f1" || records[1].FunctionID != "f2" {
		t.Errorf("records out of order or misparsed: %+v", records)
	}
}

// TestReadRunLogsRejectsMalformedLine: a run log is evidence, so quietly
// costing fewer translations than actually ran would understate the total.
func TestReadRunLogsRejectsMalformedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"job\"}\n{ this is not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadRunLogs([]string{path}); err == nil {
		t.Fatal("expected a malformed line to be reported")
	} else if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("error should point at the offending line, got: %v", err)
	}
}

func TestBreakEven(t *testing.T) {
	// a translation costing 1000 J, saving 0.5 J per invocation
	n, ok := BreakEven(1000, 1.5, 1.0)
	if !ok || math.Abs(n-2000) > 1 {
		t.Errorf("BreakEven = %v (%v), want 2000", n, ok)
	}
	// Go is not faster: a real outcome, reported rather than errored
	if _, ok := BreakEven(1000, 1.0, 1.2); ok {
		t.Error("expected no break-even when the translation is not faster")
	}
}

// TestBuildGroupsByReportingAxes covers the dataset's intended reporting:
// pass rate per complexity bucket and AWS vs non-AWS.
func TestBuildGroupsByReportingAxes(t *testing.T) {
	cfg := testConfig()
	translations := []TranslationEnergy{
		{FunctionID: "f1", Bucket: "A", UsesAWS: false, FacilityJoules: 100, TestsPassed: 3},
		{FunctionID: "f2", Bucket: "A", UsesAWS: true, FacilityJoules: 300, TestsPassed: 2, TestsFailed: 1},
		{FunctionID: "f3", Bucket: "D+", UsesAWS: true, FacilityJoules: 800, TestsFailed: 2},
	}

	report := Build(cfg, translations, map[string]RuntimeMeasurement{
		"f1": {PythonJoulesPerInvocation: 2, GoJoulesPerInvocation: 1},
		"f2": {PythonJoulesPerInvocation: 1, GoJoulesPerInvocation: 1}, // never pays back
	})

	if report.Count != 3 || math.Abs(report.MeanFacilityJoules-400) > 0.01 {
		t.Errorf("totals wrong: count=%d mean=%v", report.Count, report.MeanFacilityJoules)
	}
	if math.Abs(report.MedianFacilityJoules-300) > 0.01 {
		t.Errorf("median = %v, want 300", report.MedianFacilityJoules)
	}
	if len(report.ByBucket) != 2 || report.ByBucket[0].Group != "A" || report.ByBucket[0].Count != 2 {
		t.Errorf("bucket grouping wrong: %+v", report.ByBucket)
	}
	if len(report.ByAWSUsage) != 2 || report.ByAWSUsage[0].Group != "aws" || report.ByAWSUsage[0].Count != 2 {
		t.Errorf("aws grouping wrong: %+v", report.ByAWSUsage)
	}

	be := report.BreakEven
	if be == nil || be.Computed != 1 {
		t.Fatalf("break-even not computed: %+v", be)
	}
	if math.Abs(be.PerFunction["f1"]-100) > 0.01 {
		t.Errorf("N* for f1 = %v, want 100", be.PerFunction["f1"])
	}
	if len(be.NeverPaysBack) != 1 || be.NeverPaysBack[0] != "f2" {
		t.Errorf("expected f2 to never pay back: %+v", be.NeverPaysBack)
	}
	if len(be.Missing) != 1 || be.Missing[0] != "f3" {
		t.Errorf("expected f3 to be reported as missing runtime data: %+v", be.Missing)
	}
}

// TestReportJSONIsSelfContained: -json output feeds plotting, so it must
// carry the per-function detail and not just the summary.
func TestReportJSONIsSelfContained(t *testing.T) {
	cfg := testConfig()
	report := Build(cfg, []TranslationEnergy{
		{FunctionID: "f1", Bucket: "B", FacilityJoules: 42, Stages: []StageEnergy{{Task: "convert", Joules: 40}}},
	}, nil)

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"function_id":"f1"`, `"by_stage"`, `"total_facility_joules"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("JSON report missing %s: %s", want, data)
		}
	}
}
