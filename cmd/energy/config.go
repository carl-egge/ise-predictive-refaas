package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds every constant of EVALUATION.md section 4. It is loaded from a
// file rather than compiled in so that replacing an assumed value with a
// measured one - or sweeping it for the sensitivity table - never requires a
// code change, and so the thesis constants table has a single source of truth.
type Config struct {
	Hardware struct {
		GPUsPerNode                float64 `json:"gpus_per_node"`
		PeakFLOPSPerGPU            float64 `json:"peak_flops_per_gpu"`
		ModelFLOPUtilization       float64 `json:"model_flop_utilization"`
		HBMBandwidthBytesPerSecond float64 `json:"hbm_bandwidth_bytes_per_second"`
		AchievedBandwidthFraction  float64 `json:"achieved_bandwidth_fraction"`
		NodePowerWatts             float64 `json:"node_power_watts"`
	} `json:"hardware"`

	Models map[string]ModelConfig `json:"models"`

	Serving struct {
		Concurrency float64 `json:"concurrency"`
	} `json:"serving"`

	Facility struct {
		PUE float64 `json:"pue"`
		// GridCO2eGramsPerKWh is the location-based intensity: what the
		// electricity physically drew from the grid it sat on.
		GridCO2eGramsPerKWh float64 `json:"grid_co2e_grams_per_kwh"`
		// MarketCO2eGramsPerKWh is the market-based (contractual) intensity -
		// GWDG's own reporting basis, which their 2026-08-22 reply states is
		// carbon-neutral. It is a pointer because zero is a meaningful value
		// here and "not configured" has to stay distinguishable from it.
		MarketCO2eGramsPerKWh *float64 `json:"market_co2e_grams_per_kwh"`
	} `json:"facility"`

	// Host is the machine that runs the *pipeline* - not the inference node.
	// It builds, tests and scans, and until [H5] it contributed exactly zero
	// to E_translation, which is why cmd/energy printed 0.0 J against
	// goBuilder and goTester.
	//
	// Since 2026-09-04 the service measures this directly from RAPL and
	// records it on every job, so these constants are only a fallback for run
	// logs written before that, or produced on a host with no readable
	// counter. A figure derived from them is tagged "estimated" everywhere it
	// appears, exactly as cmd/runtime tags its -watts figures.
	Host struct {
		// FallbackPowerWatts is the whole-machine draw assumed when a job
		// carries no measurement. Zero (the default) means: report host
		// energy as unavailable rather than invent it.
		FallbackPowerWatts float64 `json:"fallback_power_watts"`
		// FallbackIdleWatts, when set, lets the fallback estimate report a
		// marginal figure alongside the gross one.
		FallbackIdleWatts float64 `json:"fallback_idle_watts"`
	} `json:"host"`

	Analysis struct {
		RepairStages []string `json:"repair_stages"`
	} `json:"analysis"`

	Sensitivity struct {
		Concurrency          []float64 `json:"concurrency"`
		NodePowerWatts       []float64 `json:"node_power_watts"`
		PeakFLOPSPerGPU      []float64 `json:"peak_flops_per_gpu"`
		BytesPerParameter    []float64 `json:"bytes_per_parameter"`
		ModelFLOPUtilization []float64 `json:"model_flop_utilization"`
		PUE                  []float64 `json:"pue"`
	} `json:"sensitivity"`
}

// ModelConfig is what the coefficients depend on for one model.
type ModelConfig struct {
	Parameters        float64 `json:"parameters"`
	BytesPerParameter float64 `json:"bytes_per_parameter"`
}

// defaultModelKey names the entry applied to any model seen in a run log that
// has no entry of its own. Without it a run using an unlisted model would
// silently contribute zero energy, which is the one failure mode this tool
// must not have.
const defaultModelKey = "_default"

// LoadConfig reads and validates the constants file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading energy config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing energy config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid energy config %s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	positive := map[string]float64{
		"hardware.gpus_per_node":                  c.Hardware.GPUsPerNode,
		"hardware.peak_flops_per_gpu":             c.Hardware.PeakFLOPSPerGPU,
		"hardware.model_flop_utilization":         c.Hardware.ModelFLOPUtilization,
		"hardware.hbm_bandwidth_bytes_per_second": c.Hardware.HBMBandwidthBytesPerSecond,
		"hardware.achieved_bandwidth_fraction":    c.Hardware.AchievedBandwidthFraction,
		"hardware.node_power_watts":               c.Hardware.NodePowerWatts,
		"serving.concurrency":                     c.Serving.Concurrency,
		"facility.pue":                            c.Facility.PUE,
	}
	for name, v := range positive {
		if v <= 0 {
			return fmt.Errorf("%s must be > 0, got %v", name, v)
		}
	}
	if _, ok := c.Models[defaultModelKey]; !ok {
		return fmt.Errorf("models.%s is required: it is what keeps a run using an unlisted model from being costed as zero", defaultModelKey)
	}
	for name, m := range c.Models {
		if m.Parameters <= 0 || m.BytesPerParameter <= 0 {
			return fmt.Errorf("models.%s needs positive parameters and bytes_per_parameter", name)
		}
	}
	return nil
}

// ModelFor returns the configuration for a model name, falling back to the
// default entry. The bool reports whether the fallback was used, so the report
// can say which models were costed on an assumption.
func (c *Config) ModelFor(name string) (ModelConfig, bool) {
	if m, ok := c.Models[name]; ok && name != defaultModelKey {
		return m, true
	}
	return c.Models[defaultModelKey], false
}

// MarketIntensity returns the market-based CO2 intensity and whether one was
// configured at all.
//
// Two intensities are reported side by side rather than one being picked,
// because neither alone is honest: GWDG states carbon-neutral operation, so
// their market-based figure is zero, but the electricity was still drawn and
// the location-based figure is what a reader comparing against other
// providers needs. This is the GHG Protocol's Scope 2 dual-reporting rule.
func (c *Config) MarketIntensity() (float64, bool) {
	if c.Facility.MarketCO2eGramsPerKWh == nil {
		return 0, false
	}
	return *c.Facility.MarketCO2eGramsPerKWh, true
}
