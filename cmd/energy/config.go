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
		PUE                 float64 `json:"pue"`
		GridCO2eGramsPerKWh float64 `json:"grid_co2e_grams_per_kwh"`
	} `json:"facility"`

	Analysis struct {
		RepairStages []string `json:"repair_stages"`
	} `json:"analysis"`

	Sensitivity struct {
		Concurrency          []float64 `json:"concurrency"`
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
