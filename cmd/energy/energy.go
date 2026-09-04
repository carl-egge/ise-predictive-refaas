package main

import (
	"sort"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// joulesPerKWh converts the model's joules into the unit grid intensity is
// quoted in.
const joulesPerKWh = 3.6e6

// Coefficients are the per-token energies of EVALUATION.md section 3, derived
// for one model on one hardware configuration.
//
// Input and output tokens are weighted separately because they are physically
// different operations: prefill processes the whole prompt in one parallel,
// compute-bound pass, while decode emits one token at a time and must stream
// the entire weight set out of GPU memory for each - making it
// memory-bandwidth-bound and dependent on how many requests share the server.
// A translation pipeline has long prompts and moderate outputs, so a single
// blended rate would distort the result materially.
type Coefficients struct {
	// EIn is joules per input (prompt) token.
	EIn float64
	// EOut is joules per output (completion) token.
	EOut float64
	// PrefillTokensPerSecond and DecodeStepSeconds are the intermediate
	// quantities, reported so a reader can check them against the hardware
	// documentation rather than trusting the end figure.
	PrefillTokensPerSecond float64
	DecodeStepSeconds      float64
}

// DeriveCoefficients implements section 3's derivation:
//
//	T_prefill = (n_gpu * peak_flops * mfu) / (2 * n_params)
//	e_in      = P_node / T_prefill
//	t_step    = weight_bytes / (n_gpu * hbm_bw * bw_eff)
//	e_out     = P_node * t_step / B
//
// The factor 2 in T_prefill is the standard two-FLOPs-per-parameter-per-token
// approximation for a forward pass (Kaplan et al., arXiv:2001.08361).
func DeriveCoefficients(hw HardwareParams, model ModelConfig, concurrency float64) Coefficients {
	prefillTokensPerSecond := (hw.GPUs * hw.PeakFLOPSPerGPU * hw.MFU) / (2 * model.Parameters)
	weightBytes := model.Parameters * model.BytesPerParameter
	decodeStep := weightBytes / (hw.GPUs * hw.HBMBandwidth * hw.BandwidthFraction)

	return Coefficients{
		EIn:                    hw.NodePowerWatts / prefillTokensPerSecond,
		EOut:                   hw.NodePowerWatts * decodeStep / concurrency,
		PrefillTokensPerSecond: prefillTokensPerSecond,
		DecodeStepSeconds:      decodeStep,
	}
}

// HardwareParams is the hardware half of the derivation, separated from the
// config struct so a sensitivity sweep can vary one field without mutating
// the loaded configuration.
type HardwareParams struct {
	GPUs              float64
	PeakFLOPSPerGPU   float64
	MFU               float64
	HBMBandwidth      float64
	BandwidthFraction float64
	NodePowerWatts    float64
}

func (c *Config) hardware() HardwareParams {
	return HardwareParams{
		GPUs:              c.Hardware.GPUsPerNode,
		PeakFLOPSPerGPU:   c.Hardware.PeakFLOPSPerGPU,
		MFU:               c.Hardware.ModelFLOPUtilization,
		HBMBandwidth:      c.Hardware.HBMBandwidthBytesPerSecond,
		BandwidthFraction: c.Hardware.AchievedBandwidthFraction,
		NodePowerWatts:    c.Hardware.NodePowerWatts,
	}
}

// StageEnergy is one pipeline stage's contribution to a translation.
type StageEnergy struct {
	Task         string  `json:"task"`
	Model        string  `json:"model,omitempty"`
	ModelAssumed bool    `json:"model_assumed,omitempty"`
	Executions   int     `json:"executions"`
	Failures     int     `json:"failures"`
	LLMCalls     int     `json:"llm_calls"`
	PromptTokens int     `json:"prompt_tokens"`
	EvalTokens   int     `json:"eval_tokens"`
	Joules       float64 `json:"joules"`
	// HostJoules is what this stage cost the machine running the pipeline
	// ([H5]). It is the only energy the non-LLM stages have: goBuilder,
	// goTester and pyScan make no inference calls, and used to appear in this
	// table as 0.0 J while running compilers, test binaries and containers.
	HostJoules float64 `json:"host_joules,omitempty"`
	IsRepair   bool    `json:"is_repair,omitempty"`
}

// TranslationEnergy is the energy of one translation run.
type TranslationEnergy struct {
	FunctionID string `json:"function_id"`
	JobID      string `json:"job_id,omitempty"`
	Bucket     string `json:"bucket,omitempty"`
	UsesAWS    bool   `json:"uses_aws"`
	// Completed distinguishes a translation from a failed attempt. Both cost
	// energy; only the former is a result, so the report separates them.
	Completed bool          `json:"completed"`
	Stages    []StageEnergy `json:"stages,omitempty"`

	PromptTokens int `json:"prompt_tokens"`
	EvalTokens   int `json:"eval_tokens"`
	// ComputeJoules is the inference energy; FacilityJoules applies PUE to it
	// and adds the host term below, and is the figure break-even uses.
	ComputeJoules  float64 `json:"compute_joules"`
	FacilityJoules float64 `json:"facility_joules"`
	CO2eGrams      float64 `json:"co2e_grams"`
	RepairJoules   float64 `json:"repair_joules"`

	// HostJoules is what the machine running the pipeline drew during this
	// conversion ([H5]) - the build, test and scan work that an inference-only
	// model costed at zero.
	//
	// HostMarginalJoules is the same window net of the host's idle draw: the
	// energy the conversion *caused*, as opposed to the energy drawn while it
	// happened to be running. The two are far apart here, because ~92% of a
	// job's wall clock is spent waiting on a remote API with this machine
	// near idle, so the report gives both rather than picking one silently -
	// the same dual-reporting the CO2 figures already use.
	HostJoules         float64 `json:"host_joules"`
	HostMarginalJoules float64 `json:"host_marginal_joules,omitempty"`
	// HostSource is "rapl" for a counter reading, "estimated" for one derived
	// from config.host.fallback_power_watts, empty when neither was available.
	HostSource string `json:"host_source,omitempty"`

	TestsPassed int `json:"tests_passed"`
	TestsFailed int `json:"tests_failed"`
	// ShapeOnlyTests counts cases judged by type only: they cannot evidence
	// value-level equivalence, and the dataset advises excluding them from
	// such claims.
	ShapeOnlyTests int            `json:"shape_only_tests"`
	FailureKinds   map[string]int `json:"failure_kinds,omitempty"`

	// Skipped marks a job the prediction gate declined ([I10]). Such a job is
	// not completed, but it is not a failed attempt either - see
	// JobRecord.IsSkipped.
	Skipped bool `json:"skipped,omitempty"`
}

// Evaluate costs one archived job record.
//
// Note that this sums per stage rather than per call: E_call is linear in the
// token counts, so summing the stage aggregates yields exactly the same total
// as per-call records would - which is why the pipeline records aggregates and
// not a row per call (see EVALUATION.md section 5).
func Evaluate(cfg *Config, rec JobRecord) TranslationEnergy {
	repair := map[string]bool{}
	for _, id := range cfg.Analysis.RepairStages {
		repair[id] = true
	}

	out := TranslationEnergy{
		FunctionID:   rec.FunctionID,
		JobID:        rec.JobID,
		Completed:    rec.IsCompleted(),
		Skipped:      rec.IsSkipped(),
		FailureKinds: map[string]int{},
	}
	if out.FunctionID == "" {
		out.FunctionID = "(unattributed)"
	}
	if rec.Metrics == nil {
		return out
	}
	if rec.Metrics.Meta != nil {
		out.Bucket = rec.Metrics.Meta.Bucket
		out.UsesAWS = rec.Metrics.Meta.AWS
	}

	for _, task := range sortedTaskIDs(rec.Metrics.PerTask) {
		tm := rec.Metrics.PerTask[task]
		if tm == nil {
			continue
		}
		model, known := cfg.ModelFor(tm.Model)
		coeff := DeriveCoefficients(cfg.hardware(), model, cfg.Serving.Concurrency)
		joules := float64(tm.PromptTokens)*coeff.EIn + float64(tm.EvalTokens)*coeff.EOut

		stage := StageEnergy{
			Task:         task,
			Model:        tm.Model,
			ModelAssumed: !known,
			Executions:   tm.Executions,
			Failures:     tm.Failures,
			LLMCalls:     tm.LLMCalls,
			PromptTokens: tm.PromptTokens,
			EvalTokens:   tm.EvalTokens,
			Joules:       joules,
			HostJoules:   tm.HostJoules,
			IsRepair:     repair[task],
		}
		out.Stages = append(out.Stages, stage)
		out.PromptTokens += tm.PromptTokens
		out.EvalTokens += tm.EvalTokens
		out.ComputeJoules += joules
		if stage.IsRepair {
			out.RepairJoules += joules
		}
	}

	hostEnergy(cfg, rec, &out)

	// PUE applies to the inference node's datacentre, not to the host running
	// the pipeline - that machine sits on a desk, not in GWDG's hall, and
	// multiplying its draw by a datacentre's cooling overhead would be an
	// invented number. So the facility figure is grossed-up inference plus the
	// host term as measured.
	out.FacilityJoules = out.ComputeJoules*cfg.Facility.PUE + out.HostJoules
	out.CO2eGrams = out.FacilityJoules / joulesPerKWh * cfg.Facility.GridCO2eGramsPerKWh

	for _, o := range rec.Metrics.TestOutcomes {
		if o.Passed {
			out.TestsPassed++
		} else {
			out.TestsFailed++
			out.FailureKinds[o.Kind]++
		}
		if o.OutputMode == "shape" {
			out.ShapeOnlyTests++
		}
	}
	return out
}

// hostSourceMeasured and hostSourceEstimated label where a host figure came
// from. The distinction has to travel with the number: one is a counter
// reading, the other is a wattage somebody typed into a config file.
const (
	hostSourceEstimated = "estimated"
)

// hostEnergy fills in the pipeline machine's contribution ([H5]).
//
// Preference order, and the reason there is one: a job recorded by a metered
// host carries a RAPL counter difference, which needs no assumption at all.
// Older run logs - every run before 2026-09-04 - carry none, so the only thing
// available is wall clock, and turning that into joules requires a stated
// wattage. When the config supplies none, the figure stays absent rather than
// becoming zero: zero would silently reinstate exactly the bug this closes.
func hostEnergy(cfg *Config, rec JobRecord, out *TranslationEnergy) {
	m := rec.Metrics
	if m == nil {
		return
	}

	if m.HostEnergySource != "" && m.HostJoules > 0 {
		out.HostJoules = m.HostJoules
		out.HostSource = m.HostEnergySource
		if m.HostIdleWatts > 0 {
			marginal := m.HostJoules - m.HostIdleWatts*m.TotalTime.Seconds()
			if marginal > 0 {
				out.HostMarginalJoules = marginal
			}
		}
		return
	}

	watts := cfg.Host.FallbackPowerWatts
	if watts <= 0 || m.TotalTime <= 0 {
		return
	}
	seconds := m.TotalTime.Seconds()
	out.HostJoules = watts * seconds
	out.HostSource = hostSourceEstimated
	if idle := cfg.Host.FallbackIdleWatts; idle > 0 && idle < watts {
		out.HostMarginalJoules = (watts - idle) * seconds
	}
}

func sortedTaskIDs(perTask map[string]*domain.TaskMetrics) []string {
	ids := make([]string, 0, len(perTask))
	for id := range perTask {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// BreakEven computes N* = E_translation / (E_python - E_go) per invocation.
// ok is false when the translation never pays back, which is a real outcome
// worth reporting rather than an error: the Go version is not faster for
// every function.
func BreakEven(translationJoules, pythonJoulesPerInvocation, goJoulesPerInvocation float64) (float64, bool) {
	saving := pythonJoulesPerInvocation - goJoulesPerInvocation
	if saving <= 0 {
		return 0, false
	}
	return translationJoules / saving, true
}
