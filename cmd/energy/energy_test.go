package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// documentedHardware is the configuration EVALUATION.md section 3 derives its
// published coefficients from, as confirmed by GWDG on 2026-08-22: Devstral is
// served on 2x H200 (141 GB HBM3e, 4.8 TB/s, 1979 TFLOP/s FP8 dense) at MFU
// 0.40. Node power remains assumed - GWDG gave no monitoring figure - at
// 2 x 700 W GPU plus a 150 W/GPU host share.
func documentedHardware() HardwareParams {
	return HardwareParams{
		GPUs:              2,
		PeakFLOPSPerGPU:   1.979e15,
		MFU:               0.40,
		HBMBandwidth:      4.8e12,
		BandwidthFraction: 0.75,
		NodePowerWatts:    1700,
	}
}

// documentedModel is Devstral 2 123B at FP8 (confirmed by GWDG, superseding
// the earlier BF16 assumption - one byte per parameter, so half the weight
// traffic per decode step).
func documentedModel() ModelConfig {
	return ModelConfig{Parameters: 123e9, BytesPerParameter: 1}
}

// supersededHardware and supersededModel are the pre-reply assumptions: 4x
// H100 PCIe at BF16. They exist so one test can show that replacing them with
// the GWDG-confirmed values changed only *constants* - the derivation itself
// still reproduces the figures the document published before the reply, which
// is what lets the thesis attribute the whole difference to better inputs
// rather than to a changed method.
func supersededHardware() HardwareParams {
	return HardwareParams{
		GPUs:              4,
		PeakFLOPSPerGPU:   756e12,
		MFU:               0.40,
		HBMBandwidth:      2.0e12,
		BandwidthFraction: 0.75,
		NodePowerWatts:    2000,
	}
}

func supersededModel() ModelConfig {
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

	// T_prefill = (2 * 1979e12 * 0.40) / (2 * 123e9) ~ 6,440 tokens/s
	closeTo(t, "prefill tokens/s", c.PrefillTokensPerSecond, 6440, 50)
	// e_in = 1700 W / 6440 ~ 0.264 J per input token
	closeTo(t, "e_in", c.EIn, 0.264, 0.005)
	// t_step = 123 GB / (2 * 4.8e12 * 0.75) ~ 17.1 ms
	closeTo(t, "decode step (ms)", c.DecodeStepSeconds*1000, 17.1, 0.3)
	// e_out at B=32 ~ 0.91 J per output token
	closeTo(t, "e_out", c.EOut, 0.908, 0.02)
}

// TestSupersededCoefficientsStillDerive pins the pre-GWDG-reply figures
// (4x H100 PCIe, BF16: e_in 0.41, e_out 2.6 at B=32) against the *current*
// formula.
//
// The point is not that those numbers are still used - they are not - but that
// the GWDG reply changed inputs only. If this fails alongside the test above,
// the derivation itself drifted, and the thesis can no longer say the drop
// from ~9.3 Wh to ~4.3 Wh per translation is what better hardware data bought.
func TestSupersededCoefficientsStillDerive(t *testing.T) {
	c := DeriveCoefficients(supersededHardware(), supersededModel(), 32)

	closeTo(t, "superseded prefill tokens/s", c.PrefillTokensPerSecond, 4900, 50)
	closeTo(t, "superseded e_in", c.EIn, 0.41, 0.01)
	closeTo(t, "superseded decode step (ms)", c.DecodeStepSeconds*1000, 41, 0.5)
	closeTo(t, "superseded e_out", c.EOut, 2.6, 0.05)
}

// TestEOutScalesWithConcurrency reproduces the concurrency table of section 3.
// B is the single largest unknown in the model, so the relationship it enters
// through is worth pinning.
func TestEOutScalesWithConcurrency(t *testing.T) {
	for _, tc := range []struct {
		concurrency float64
		wantEOut    float64
	}{
		{8, 3.63},
		{16, 1.82},
		{32, 0.91},
		{64, 0.45},
		{128, 0.23},
	} {
		c := DeriveCoefficients(documentedHardware(), documentedModel(), tc.concurrency)
		closeTo(t, fmt.Sprintf("e_out at B=%.0f", tc.concurrency), c.EOut, tc.wantEOut, 0.02)
	}
}

// TestWorkedExample reproduces section 3's worked example end to end: five
// calls averaging 6,000 prompt and 1,500 output tokens come to roughly 15.5 kJ
// (~4.3 Wh) of facility energy under the GWDG-confirmed configuration.
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

	closeTo(t, "compute joules", got.ComputeJoules, 14732, 300)
	closeTo(t, "facility joules", got.FacilityJoules, 15469, 300)
	// and the location-based CO2e conversion: 15.5 kJ = 4.3 Wh at 363 g/kWh
	closeTo(t, "co2e grams", got.CO2eGrams, 1.56, 0.05)
}

func testConfig() *Config {
	hw := documentedHardware()
	cfg := &Config{}
	cfg.Hardware.GPUsPerNode = hw.GPUs
	cfg.Hardware.PeakFLOPSPerGPU = hw.PeakFLOPSPerGPU
	cfg.Hardware.ModelFLOPUtilization = hw.MFU
	cfg.Hardware.HBMBandwidthBytesPerSecond = hw.HBMBandwidth
	cfg.Hardware.AchievedBandwidthFraction = hw.BandwidthFraction
	cfg.Hardware.NodePowerWatts = hw.NodePowerWatts
	cfg.Serving.Concurrency = 32
	cfg.Facility.PUE = 1.05
	cfg.Facility.GridCO2eGramsPerKWh = 363
	market := 0.0
	cfg.Facility.MarketCO2eGramsPerKWh = &market
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
	closeTo(t, "shipped config e_in", c.EIn, 0.264, 0.005)
	closeTo(t, "shipped config e_out", c.EOut, 0.908, 0.02)
	// FP8 is the one hardware fact GWDG stated outright about this model; a
	// config that quietly reverted to BF16 would double every decode cost
	// while still looking entirely plausible.
	if got := cfg.Models[defaultModelKey].BytesPerParameter; got != 1 {
		t.Errorf("bytes_per_parameter = %v, want 1 (GWDG confirmed FP8 for Devstral)", got)
	}
	if len(cfg.Sensitivity.Concurrency) == 0 {
		t.Error("the sensitivity sweep needs a concurrency range to produce the section 8 table")
	}
	// GWDG declined to release throughput/concurrency and gave no node-power
	// figure, so these two sweeps are what stands in for the missing answers.
	if len(cfg.Sensitivity.NodePowerWatts) == 0 || len(cfg.Sensitivity.PeakFLOPSPerGPU) == 0 {
		t.Error("node power and prefill peak must be swept: GWDG supplied neither")
	}
	if _, ok := cfg.MarketIntensity(); !ok {
		t.Error("the shipped config must carry a market-based intensity: GWDG reports carbon-neutral operation, and the report states both figures")
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
		{FunctionID: "f1", Bucket: "A", UsesAWS: false, FacilityJoules: 100, TestsPassed: 3, Completed: true},
		{FunctionID: "f2", Bucket: "A", UsesAWS: true, FacilityJoules: 300, TestsPassed: 2, TestsFailed: 1, Completed: true},
		{FunctionID: "f3", Bucket: "D+", UsesAWS: true, FacilityJoules: 800, TestsFailed: 2, Completed: true},
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

// --- [H2] failed attempts are archived, costed, and reported apart ----------

// TestJobRecordTreatsAbsentCompletedAsSuccess is the back-compatibility rule
// that keeps historical run logs readable: they predate the completed flag and
// contain nothing but completed jobs, so decoding a missing field into Go's
// zero value would reclassify every past translation as a failure and empty
// every table in the report.
func TestJobRecordTreatsAbsentCompletedAsSuccess(t *testing.T) {
	var legacy JobRecord
	if err := json.Unmarshal([]byte(`{"type":"job","function_id":"f1"}`), &legacy); err != nil {
		t.Fatalf("decoding a pre-flag record: %v", err)
	}
	if !legacy.IsCompleted() {
		t.Error("a record without the completed field must be read as completed")
	}

	var failed JobRecord
	if err := json.Unmarshal([]byte(`{"type":"job","function_id":"f2","completed":false}`), &failed); err != nil {
		t.Fatalf("decoding a failed record: %v", err)
	}
	if failed.IsCompleted() {
		t.Error("completed:false must be read as a failed attempt, not confused with absent")
	}
}

// TestBuildSeparatesFailedAttempts: failures must not move any per-translation
// figure, but their energy must still be visible - that is the whole point of
// archiving them ([H2]).
func TestBuildSeparatesFailedAttempts(t *testing.T) {
	cfg := testConfig()
	jobs := []TranslationEnergy{
		{FunctionID: "f1", Bucket: "A", FacilityJoules: 100, TestsPassed: 2, PromptTokens: 10, Completed: true},
		{FunctionID: "f2", Bucket: "A", FacilityJoules: 300, TestsPassed: 1, PromptTokens: 30, Completed: true},
		{FunctionID: "f9", Bucket: "B", FacilityJoules: 600, TestsFailed: 3, PromptTokens: 60, CO2eGrams: 1.5},
	}

	report := Build(cfg, jobs, nil)

	// the headline figures describe the two completed translations only
	if report.Count != 2 {
		t.Errorf("Count = %d, want 2 completed translations", report.Count)
	}
	if math.Abs(report.TotalFacilityJoules-400) > 0.01 {
		t.Errorf("total energy = %v, want 400 (failures excluded)", report.TotalFacilityJoules)
	}
	if math.Abs(report.MeanFacilityJoules-200) > 0.01 {
		t.Errorf("mean = %v, want 200 - a failed attempt must not enter the denominator", report.MeanFacilityJoules)
	}
	if report.TotalPromptTokens != 40 {
		t.Errorf("prompt tokens = %d, want 40 (failures excluded)", report.TotalPromptTokens)
	}
	for _, g := range report.ByBucket {
		if g.Group == "B" {
			t.Errorf("a failed attempt must not appear as a reporting group: %+v", g)
		}
	}

	// ...and the failure is still fully accounted for
	f := report.FailedAttempts
	if f == nil {
		t.Fatal("failed attempts must be reported, not dropped")
	}
	if f.Count != 1 || len(f.FunctionIDs) != 1 || f.FunctionIDs[0] != "f9" {
		t.Errorf("failed attempt not identified: %+v", f)
	}
	if math.Abs(f.FacilityJoules-600) > 0.01 || f.PromptTokens != 60 {
		t.Errorf("failed attempt cost wrong: %+v", f)
	}
	// 600 wasted of 1000 spent
	if math.Abs(f.ShareOfTotalSpend-0.6) > 0.001 {
		t.Errorf("share of total spend = %v, want 0.6", f.ShareOfTotalSpend)
	}
	// 1000 J spent to obtain 2 working translations
	if math.Abs(f.JoulesPerSuccess-500) > 0.01 {
		t.Errorf("cost per success = %v, want 500 with failures amortized in", f.JoulesPerSuccess)
	}
	if len(f.Translations) != 1 {
		t.Errorf("the costed failures themselves must survive for -json consumers: %+v", f.Translations)
	}
}

// TestBuildWithoutFailuresReportsNone keeps a clean run clean: no failure
// section, and nothing that could be mistaken for one.
func TestBuildWithoutFailuresReportsNone(t *testing.T) {
	report := Build(testConfig(), []TranslationEnergy{
		{FunctionID: "f1", FacilityJoules: 100, Completed: true},
	}, nil)
	if report.FailedAttempts != nil {
		t.Errorf("a run with no failures must report none, got %+v", report.FailedAttempts)
	}
}

// TestBuildAllFailedStillAccountsEnergy: a batch where nothing completed must
// still say what it cost, rather than reporting an empty run.
func TestBuildAllFailedStillAccountsEnergy(t *testing.T) {
	report := Build(testConfig(), []TranslationEnergy{
		{FunctionID: "f9", FacilityJoules: 600},
	}, nil)

	if report.Count != 0 {
		t.Errorf("Count = %d, want 0 completed translations", report.Count)
	}
	if report.FailedAttempts == nil || math.Abs(report.FailedAttempts.FacilityJoules-600) > 0.01 {
		t.Fatalf("energy spent on a fully failed batch must still be reported: %+v", report.FailedAttempts)
	}
	// no completed translations means no denominator; it must not divide by zero
	if report.FailedAttempts.JoulesPerSuccess != 0 {
		t.Errorf("cost per success has no meaning with zero successes, got %v",
			report.FailedAttempts.JoulesPerSuccess)
	}
}

// --- GWDG carbon-neutrality reply: both intensities are reported -----------

// TestReportStatesBothCO2Intensities covers the GWDG reply's carbon-neutrality
// statement: the provider's own basis is zero, but the electricity was still
// drawn, so the report must carry both figures rather than silently picking
// one (GHG Protocol Scope 2 dual reporting).
func TestReportStatesBothCO2Intensities(t *testing.T) {
	cfg := testConfig()
	report := Build(cfg, []TranslationEnergy{
		{FunctionID: "f1", FacilityJoules: 3.6e6, CO2eGrams: 363, Completed: true},
	}, nil)

	if report.TotalCO2eGramsMarket == nil {
		t.Fatal("a configured market intensity must produce a market-based total")
	}
	if *report.TotalCO2eGramsMarket != 0 {
		t.Errorf("market-based total = %v, want 0 at a carbon-neutral intensity", *report.TotalCO2eGramsMarket)
	}
	// 1 kWh at the German grid average, unchanged by the market figure
	closeTo(t, "location-based total", report.TotalCO2eGrams, 363, 0.5)

	var buf strings.Builder
	report.Write(&buf, cfg)
	for _, want := range []string{"location-based", "market-based"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("report must state the %s figure:\n%s", want, buf.String())
		}
	}
}

// TestReportOmitsMarketCO2WhenNotConfigured: a provider that makes no
// carbon-neutrality claim must not be handed a fabricated zero.
func TestReportOmitsMarketCO2WhenNotConfigured(t *testing.T) {
	cfg := testConfig()
	cfg.Facility.MarketCO2eGramsPerKWh = nil

	report := Build(cfg, []TranslationEnergy{{FunctionID: "f1", FacilityJoules: 3.6e6, Completed: true}}, nil)
	if report.TotalCO2eGramsMarket != nil {
		t.Errorf("unconfigured market intensity must stay absent, got %v", *report.TotalCO2eGramsMarket)
	}

	var buf strings.Builder
	report.Write(&buf, cfg)
	if strings.Contains(buf.String(), "market-based") {
		t.Errorf("no market intensity configured, so the report must not claim one:\n%s", buf.String())
	}
}
