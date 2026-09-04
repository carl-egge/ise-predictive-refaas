package main

import (
	"strings"
	"testing"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

func hostTestConfig(fallbackWatts, idleWatts float64) *Config {
	cfg := &Config{}
	cfg.Hardware.GPUsPerNode = 2
	cfg.Hardware.PeakFLOPSPerGPU = 1.979e15
	cfg.Hardware.ModelFLOPUtilization = 0.4
	cfg.Hardware.HBMBandwidthBytesPerSecond = 4.8e12
	cfg.Hardware.AchievedBandwidthFraction = 0.75
	cfg.Hardware.NodePowerWatts = 1700
	cfg.Models = map[string]ModelConfig{
		defaultModelKey: {Parameters: 1.23e11, BytesPerParameter: 1},
	}
	cfg.Serving.Concurrency = 32
	cfg.Facility.PUE = 1.05
	cfg.Facility.GridCO2eGramsPerKWh = 363
	cfg.Host.FallbackPowerWatts = fallbackWatts
	cfg.Host.FallbackIdleWatts = idleWatts
	return cfg
}

func hostTestRecord(m *domain.Metrics) JobRecord {
	completed := true
	return JobRecord{FunctionID: "f1", Completed: &completed, Metrics: m}
}

// TestMeasuredHostEnergyIsPreferredOverTheFallback is the core of [H5]: when
// the run log carries a counter reading, no configured wattage may influence
// the answer.
func TestMeasuredHostEnergyIsPreferredOverTheFallback(t *testing.T) {
	cfg := hostTestConfig(25, 11)
	m := &domain.Metrics{
		TotalTime:        100 * time.Second,
		HostJoules:       1234,
		HostEnergySource: "rapl",
		HostIdleWatts:    10,
	}

	got := Evaluate(cfg, hostTestRecord(m))
	if got.HostJoules != 1234 {
		t.Errorf("host joules = %v, want the measured 1234 (not 25 W x 100 s = 2500)", got.HostJoules)
	}
	if got.HostSource != "rapl" {
		t.Errorf("host source = %q, want rapl", got.HostSource)
	}
	// 1234 J drawn, of which 10 W x 100 s = 1000 J would have been drawn anyway.
	if got.HostMarginalJoules < 233 || got.HostMarginalJoules > 235 {
		t.Errorf("marginal host joules = %v, want ~234", got.HostMarginalJoules)
	}
	// And it must actually reach the headline figure.
	if got.FacilityJoules <= got.ComputeJoules*cfg.Facility.PUE {
		t.Error("host energy did not reach FacilityJoules; E_translation is still inference-only")
	}
}

// TestUnmeteredJobReportsNoHostEnergyRatherThanZero pins the honesty rule.
// A zero here is indistinguishable from "the build stages were free", which is
// the exact claim this work exists to stop making.
func TestUnmeteredJobReportsNoHostEnergyRatherThanZero(t *testing.T) {
	cfg := hostTestConfig(0, 0) // no fallback configured
	m := &domain.Metrics{TotalTime: 100 * time.Second}

	got := Evaluate(cfg, hostTestRecord(m))
	if got.HostSource != "" {
		t.Errorf("host source = %q, want empty for an unmetered job with no fallback", got.HostSource)
	}
	if got.HostJoules != 0 {
		t.Errorf("host joules = %v, want 0 alongside an empty source", got.HostJoules)
	}
}

// TestFallbackHostEnergyIsTaggedEstimated covers re-costing a run log written
// before host metering existed - every run up to 20260831-190900.
func TestFallbackHostEnergyIsTaggedEstimated(t *testing.T) {
	cfg := hostTestConfig(25, 11)
	m := &domain.Metrics{TotalTime: 200 * time.Second}

	got := Evaluate(cfg, hostTestRecord(m))
	if got.HostSource != hostSourceEstimated {
		t.Errorf("host source = %q, want %q - a derived figure must not pass as measured",
			got.HostSource, hostSourceEstimated)
	}
	if got.HostJoules != 5000 { // 25 W x 200 s
		t.Errorf("host joules = %v, want 5000", got.HostJoules)
	}
	if got.HostMarginalJoules != 2800 { // (25-11) W x 200 s
		t.Errorf("marginal host joules = %v, want 2800", got.HostMarginalJoules)
	}
}

// TestSharesAreOfInferenceNotOfTheTotal guards the arithmetic the host term
// would otherwise corrupt: repair share and per-stage share used to recover
// compute joules by dividing the facility total by PUE, which stops being
// correct the moment anything else joins that total.
func TestSharesAreOfInferenceNotOfTheTotal(t *testing.T) {
	cfg := hostTestConfig(0, 0)
	withHost := &domain.Metrics{
		TotalTime:        100 * time.Second,
		HostJoules:       50000, // deliberately large relative to inference
		HostEnergySource: "rapl",
		PerTask: map[string]*domain.TaskMetrics{
			"convert":       {Executions: 1, PromptTokens: 1000, EvalTokens: 1000},
			"gollmRecovery": {Executions: 1, PromptTokens: 1000, EvalTokens: 1000},
			"goBuilder":     {Executions: 1, HostJoules: 40000},
		},
	}
	cfg.Analysis.RepairStages = []string{"gollmRecovery"}

	rep := Build(cfg, []TranslationEnergy{Evaluate(cfg, hostTestRecord(withHost))}, nil)

	// convert and gollmRecovery consumed identical tokens, so repair is
	// exactly half of inference regardless of how large the host term is.
	if rep.RepairShare < 0.49 || rep.RepairShare > 0.51 {
		t.Errorf("repair share = %.3f, want ~0.5 - it must be a share of inference, not of the total",
			rep.RepairShare)
	}
	var stageShare float64
	for _, s := range rep.ByStage {
		if s.Task == "convert" {
			stageShare = s.Share
			if s.HostJoules != 0 {
				t.Errorf("convert reported %v host joules, want 0", s.HostJoules)
			}
		}
		if s.Task == "goBuilder" && s.HostJoules != 40000 {
			t.Errorf("goBuilder host joules = %v, want 40000 - a build stage's energy is entirely host-side",
				s.HostJoules)
		}
	}
	if stageShare < 0.49 || stageShare > 0.51 {
		t.Errorf("convert stage share = %.3f, want ~0.5", stageShare)
	}
}

// TestReportFlagsAMixedProvenance: half measured and half derived is not a
// measured set, and the report must not round that away.
func TestReportFlagsAMixedProvenance(t *testing.T) {
	cfg := hostTestConfig(25, 0)
	measured := &domain.Metrics{TotalTime: time.Second, HostJoules: 10, HostEnergySource: "rapl"}
	derived := &domain.Metrics{TotalTime: time.Second}

	rep := Build(cfg, []TranslationEnergy{
		Evaluate(cfg, hostTestRecord(measured)),
		Evaluate(cfg, hostTestRecord(derived)),
	}, nil)
	if rep.HostSource != "mixed" {
		t.Errorf("host source = %q, want mixed", rep.HostSource)
	}

	var sb strings.Builder
	rep.Write(&sb, cfg)
	if !strings.Contains(sb.String(), "ESTIMATED") && !strings.Contains(sb.String(), "mixed") {
		t.Errorf("the report hides a partly-derived host figure:\n%s", sb.String())
	}
}
