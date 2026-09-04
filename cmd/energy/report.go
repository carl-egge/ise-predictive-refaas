package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Report is the whole analysis of a set of run logs, in a shape that is both
// printed and (with -json) machine readable for plotting.
type Report struct {
	// Translations holds the completed translations every figure below is
	// computed from. Failed attempts are costed too but kept apart, in
	// FailedAttempts - they spent energy without producing a result, so
	// folding them into a per-function mean or a pass rate would misreport
	// both (TODO.md [H2]).
	Translations []TranslationEnergy `json:"translations"`

	// FailedAttempts accounts for the jobs that never produced a translation.
	// Nil when the run logs contain none.
	FailedAttempts *FailedAttempts `json:"failed_attempts,omitempty"`

	// Skipped accounts for jobs the ex-ante prediction gate declined ([I10]).
	// Kept apart from FailedAttempts because a skip is a decision, not a
	// failure: it spent no tokens, and folding it in would add a free failure
	// that flatters every cost-per-success figure. Nil when the run logs
	// contain none, which is every run made without the gate.
	Skipped *SkippedAttempts `json:"skipped,omitempty"`

	Count                int     `json:"count"`
	TotalFacilityJoules  float64 `json:"total_facility_joules"`
	MeanFacilityJoules   float64 `json:"mean_facility_joules"`
	MedianFacilityJoules float64 `json:"median_facility_joules"`
	// TotalComputeJoules is the inference term alone, before PUE and before
	// the host term. Kept explicitly because the repair and per-stage shares
	// are shares *of inference*, and deriving it back out of the facility
	// total by dividing by PUE stopped being correct once the host term
	// joined that total ([H5]).
	TotalComputeJoules float64 `json:"total_compute_joules"`
	// TotalHostJoules is what the pipeline's own machine drew across these
	// translations, and TotalHostMarginalJoules is that net of its idle
	// baseline - the part the conversions actually caused.
	TotalHostJoules         float64 `json:"total_host_joules"`
	TotalHostMarginalJoules float64 `json:"total_host_marginal_joules,omitempty"`
	// HostSource is "rapl" when every translation carried a counter reading,
	// "estimated" when the figure came from a configured wattage, "mixed"
	// when the set contains both, and empty when no host energy is known.
	HostSource string `json:"host_source,omitempty"`
	// TranslationsWithoutHostEnergy counts translations contributing no host
	// term at all, so a partially-metered run cannot look fully measured.
	TranslationsWithoutHostEnergy int `json:"translations_without_host_energy,omitempty"`
	// TotalCO2eGrams is location-based: the German grid intensity applied to
	// the energy actually drawn. TotalCO2eGramsMarket is the market-based
	// counterpart under the provider's own procurement (zero for GWDG, who
	// state carbon-neutral operation) and is nil when the config names no
	// market intensity. Both are reported; see Config.MarketIntensity.
	TotalCO2eGrams       float64  `json:"total_co2e_grams"`
	TotalCO2eGramsMarket *float64 `json:"total_co2e_grams_market,omitempty"`
	TotalPromptTokens    int      `json:"total_prompt_tokens"`
	TotalEvalTokens      int      `json:"total_eval_tokens"`
	RepairShare          float64  `json:"repair_share"`

	ByStage    []StageAggregate `json:"by_stage"`
	ByBucket   []GroupAggregate `json:"by_bucket"`
	ByAWSUsage []GroupAggregate `json:"by_aws_usage"`

	BreakEven *BreakEvenReport `json:"break_even,omitempty"`

	// AssumedModels lists models costed with the default coefficients because
	// the config had no entry for them - a caveat the reader must see.
	AssumedModels []string `json:"assumed_models,omitempty"`
}

// FailedAttempts accounts for the jobs that spent energy without producing a
// translation, so a run's total spend is visible and the cost of a *successful*
// translation can be stated with its failures amortized in.
//
// This is the half of the picture the run log used to discard entirely. In the
// first full batch it was the larger half: six of fourteen jobs, holding 68% of
// the inference energy, because a job that fails is usually one that exhausted
// its repair budget first.
type FailedAttempts struct {
	Count          int      `json:"count"`
	FunctionIDs    []string `json:"function_ids,omitempty"`
	PromptTokens   int      `json:"prompt_tokens"`
	EvalTokens     int      `json:"eval_tokens"`
	FacilityJoules float64  `json:"facility_joules"`
	CO2eGrams      float64  `json:"co2e_grams"`
	// ShareOfTotalSpend is this energy over the run's whole spend (completed
	// plus failed).
	ShareOfTotalSpend float64 `json:"share_of_total_spend"`
	// JoulesPerSuccess is the run's total spend divided by the number of
	// completed translations: what one working Go function actually cost,
	// including the attempts that had to be paid for on the way. Zero when
	// nothing completed.
	JoulesPerSuccess float64 `json:"joules_per_success"`
	// Translations carries the costed failures themselves, so -json consumers
	// can see everything the run logs recorded rather than a summary.
	Translations []TranslationEnergy `json:"translations,omitempty"`
}

// StageAggregate is one pipeline stage summed across translations.
type StageAggregate struct {
	Task   string  `json:"task"`
	Joules float64 `json:"joules"`
	// HostJoules is this stage's cost on the machine running the pipeline
	// ([H5]). For goBuilder/goTester/pyScan it is the stage's entire energy:
	// they make no inference calls, and before this existed they appeared in
	// the table at 0.0 J while compiling and running test binaries.
	HostJoules float64 `json:"host_joules,omitempty"`
	Share      float64 `json:"share"`
	Executions int     `json:"executions"`
	Failures   int     `json:"failures"`
	LLMCalls   int     `json:"llm_calls"`
	IsRepair   bool    `json:"is_repair"`
}

// writeHostEnergy renders the pipeline machine's contribution ([H5]).
//
// It prints even when nothing is known, because "not counted" is the finding
// this section exists to stop being invisible: for months E_translation was
// inference alone, and the per-stage table showed 0.0 J against the stages
// that run compilers and test binaries.
func (r *Report) writeHostEnergy(w io.Writer) {
	if r.HostSource == "" {
		fmt.Fprintf(w, "    of which:    %s LLM inference, host energy NOT COUNTED\n",
			formatJoules(r.TotalFacilityJoules))
		fmt.Fprintln(w, "                 (this run log predates host metering; set host.fallback_power_watts to estimate it)")
		return
	}

	label := "measured via " + r.HostSource
	if r.HostSource == hostSourceEstimated {
		label = "ESTIMATED from a configured wattage, not measured"
	}
	// Inference-with-PUE is the total minus the host term rather than a
	// re-multiplication, so the two lines always add back to the headline.
	fmt.Fprintf(w, "    of which:    %s LLM inference (incl. PUE) + %s pipeline host (%s)\n",
		formatJoules(r.TotalFacilityJoules-r.TotalHostJoules),
		formatJoules(r.TotalHostJoules), label)
	if r.TotalHostMarginalJoules > 0 {
		fmt.Fprintf(w, "                 host above idle: %s - the rest was drawn waiting on the LLM API\n",
			formatJoules(r.TotalHostMarginalJoules))
	}
	if r.TranslationsWithoutHostEnergy > 0 {
		fmt.Fprintf(w, "                 WARNING: %d translation(s) carry no host energy; the total is a lower bound\n",
			r.TranslationsWithoutHostEnergy)
	}
}

// combinedHostSource collapses the per-translation provenance into one label.
// "mixed" is deliberately not smoothed away: a set half measured and half
// derived from a configured wattage is not a measured set, and reporting it as
// one would be the same category of error as costing build stages at zero.
func combinedHostSource(sources map[string]bool) string {
	switch len(sources) {
	case 0:
		return ""
	case 1:
		for s := range sources {
			return s
		}
	}
	return "mixed"
}

// GroupAggregate summarises one reporting axis (complexity bucket, AWS usage).
type GroupAggregate struct {
	Group       string  `json:"group"`
	Count       int     `json:"count"`
	MeanJoules  float64 `json:"mean_joules"`
	TestsPassed int     `json:"tests_passed"`
	TestsFailed int     `json:"tests_failed"`
	ShapeOnly   int     `json:"shape_only_tests"`
}

// BreakEvenReport is the primary result: after how many invocations a
// translation has paid for itself.
type BreakEvenReport struct {
	Computed      int                `json:"computed"`
	NeverPaysBack []string           `json:"never_pays_back,omitempty"`
	Missing       []string           `json:"missing_runtime_data,omitempty"`
	PerFunction   map[string]float64 `json:"per_function"`
	Median        float64            `json:"median"`
	Min           float64            `json:"min"`
	Max           float64            `json:"max"`
}

// Build assembles the report from costed jobs.
//
// Completed translations drive every figure; failed attempts are summarised
// separately by splitFailed. Both are kept, because the energy they cost was
// really spent - see FailedAttempts.
func Build(cfg *Config, jobs []TranslationEnergy, runtime map[string]RuntimeMeasurement) *Report {
	translations, failed, skipped := splitFailed(jobs)

	r := &Report{Translations: translations, Count: len(translations)}
	r.FailedAttempts = summariseFailed(failed)
	r.Skipped = summariseSkipped(skipped)
	if len(translations) == 0 {
		// Nothing completed: the failure summary is the only result there is,
		// and its per-success figure has no denominator.
		return r
	}

	stages := map[string]*StageAggregate{}
	assumed := map[string]bool{}
	buckets := map[string]*GroupAggregate{}
	aws := map[string]*GroupAggregate{}
	var repairJoules float64
	facility := make([]float64, 0, len(translations))

	hostSources := map[string]bool{}
	for _, t := range translations {
		r.TotalFacilityJoules += t.FacilityJoules
		r.TotalComputeJoules += t.ComputeJoules
		r.TotalCO2eGrams += t.CO2eGrams
		r.TotalPromptTokens += t.PromptTokens
		r.TotalEvalTokens += t.EvalTokens
		repairJoules += t.RepairJoules
		facility = append(facility, t.FacilityJoules)

		r.TotalHostJoules += t.HostJoules
		r.TotalHostMarginalJoules += t.HostMarginalJoules
		if t.HostSource == "" {
			r.TranslationsWithoutHostEnergy++
		} else {
			hostSources[t.HostSource] = true
		}

		for _, s := range t.Stages {
			agg := stages[s.Task]
			if agg == nil {
				agg = &StageAggregate{Task: s.Task, IsRepair: s.IsRepair}
				stages[s.Task] = agg
			}
			agg.Joules += s.Joules
			agg.HostJoules += s.HostJoules
			agg.Executions += s.Executions
			agg.Failures += s.Failures
			agg.LLMCalls += s.LLMCalls
			// Only a stage that consumed tokens can be mis-costed by a missing
			// config entry; its joules are derived from them. Build/test
			// stages report no model because they call no LLM, and flagging
			// those raised a permanent "(unrecorded)" warning on every run -
			// which reads as "the configured coefficients were not applied"
			// when they were, on a run where every LLM stage was costed
			// correctly.
			if s.ModelAssumed && (s.PromptTokens > 0 || s.EvalTokens > 0) {
				name := s.Model
				if name == "" {
					// pre-[H3] records, or a connector that reported none
					name = "(unrecorded)"
				}
				assumed[name] = true
			}
		}

		addGroup(buckets, bucketLabel(t.Bucket), t)
		addGroup(aws, awsLabel(t.UsesAWS), t)
	}

	r.MeanFacilityJoules = r.TotalFacilityJoules / float64(len(translations))
	r.MedianFacilityJoules = median(facility)
	if intensity, ok := cfg.MarketIntensity(); ok {
		// linear in energy, so deriving it here is exactly equivalent to
		// costing every translation twice
		market := r.TotalFacilityJoules / joulesPerKWh * intensity
		r.TotalCO2eGramsMarket = &market
	}
	r.HostSource = combinedHostSource(hostSources)
	if r.TotalComputeJoules > 0 {
		// repair energy is inference-side, so compare like with like
		r.RepairShare = repairJoules / r.TotalComputeJoules
	}

	for _, agg := range stages {
		if r.TotalComputeJoules > 0 {
			agg.Share = agg.Joules / r.TotalComputeJoules
		}
		r.ByStage = append(r.ByStage, *agg)
	}
	sort.Slice(r.ByStage, func(i, j int) bool { return r.ByStage[i].Joules > r.ByStage[j].Joules })

	r.ByBucket = flattenGroups(buckets)
	r.ByAWSUsage = flattenGroups(aws)
	for m := range assumed {
		r.AssumedModels = append(r.AssumedModels, m)
	}
	sort.Strings(r.AssumedModels)

	if len(runtime) > 0 {
		r.BreakEven = buildBreakEven(translations, runtime)
	}
	if r.FailedAttempts != nil {
		total := r.TotalFacilityJoules + r.FailedAttempts.FacilityJoules
		if total > 0 {
			r.FailedAttempts.ShareOfTotalSpend = r.FailedAttempts.FacilityJoules / total
		}
		r.FailedAttempts.JoulesPerSuccess = total / float64(len(translations))
	}
	return r
}

// splitFailed partitions costed jobs into completed translations, failed
// attempts and gate-declined candidates, preserving input order within each.
//
// The skip test comes first because a skipped job is never completed: checking
// completion first would be correct today but would silently reclassify skips
// the moment a gate is ever placed after a stage that can produce output.
func splitFailed(jobs []TranslationEnergy) (completed, failed, skipped []TranslationEnergy) {
	for _, j := range jobs {
		switch {
		case j.Skipped:
			skipped = append(skipped, j)
		case j.Completed:
			completed = append(completed, j)
		default:
			failed = append(failed, j)
		}
	}
	return completed, failed, skipped
}

// SkippedAttempts accounts for candidates the prediction gate declined.
type SkippedAttempts struct {
	Count          int      `json:"count"`
	FunctionIDs    []string `json:"function_ids"`
	FacilityJoules float64  `json:"facility_joules"`
}

// summariseSkipped totals what the gate declined. Returns nil when there were
// none, so a run made without the gate prints no section at all.
func summariseSkipped(skipped []TranslationEnergy) *SkippedAttempts {
	if len(skipped) == 0 {
		return nil
	}
	out := &SkippedAttempts{Count: len(skipped), FunctionIDs: make([]string, 0, len(skipped))}
	for _, s := range skipped {
		out.FunctionIDs = append(out.FunctionIDs, s.FunctionID)
		out.FacilityJoules += s.FacilityJoules
	}
	return out
}

// summariseFailed totals the energy spent on jobs that produced no
// translation. Returns nil when there were none, so a clean run prints no
// failure section at all.
func summariseFailed(failed []TranslationEnergy) *FailedAttempts {
	if len(failed) == 0 {
		return nil
	}
	out := &FailedAttempts{Count: len(failed), Translations: failed}
	for _, f := range failed {
		out.FunctionIDs = append(out.FunctionIDs, f.FunctionID)
		out.PromptTokens += f.PromptTokens
		out.EvalTokens += f.EvalTokens
		out.FacilityJoules += f.FacilityJoules
		out.CO2eGrams += f.CO2eGrams
	}
	return out
}

func addGroup(groups map[string]*GroupAggregate, key string, t TranslationEnergy) {
	agg := groups[key]
	if agg == nil {
		agg = &GroupAggregate{Group: key}
		groups[key] = agg
	}
	agg.Count++
	agg.MeanJoules += t.FacilityJoules // summed here, divided in flattenGroups
	agg.TestsPassed += t.TestsPassed
	agg.TestsFailed += t.TestsFailed
	agg.ShapeOnly += t.ShapeOnlyTests
}

func flattenGroups(groups map[string]*GroupAggregate) []GroupAggregate {
	out := make([]GroupAggregate, 0, len(groups))
	for _, agg := range groups {
		if agg.Count > 0 {
			agg.MeanJoules /= float64(agg.Count)
		}
		out = append(out, *agg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

func bucketLabel(bucket string) string {
	if bucket == "" {
		return "(no meta.json)"
	}
	return bucket
}

func awsLabel(usesAWS bool) string {
	if usesAWS {
		return "aws"
	}
	return "non-aws"
}

func buildBreakEven(translations []TranslationEnergy, runtime map[string]RuntimeMeasurement) *BreakEvenReport {
	out := &BreakEvenReport{PerFunction: map[string]float64{}}
	values := make([]float64, 0, len(translations))

	for _, t := range translations {
		rt, ok := runtime[t.FunctionID]
		if !ok {
			out.Missing = append(out.Missing, t.FunctionID)
			continue
		}
		n, pays := BreakEven(t.FacilityJoules, rt.PythonJoulesPerInvocation, rt.GoJoulesPerInvocation)
		if !pays {
			out.NeverPaysBack = append(out.NeverPaysBack, t.FunctionID)
			continue
		}
		out.PerFunction[t.FunctionID] = n
		values = append(values, n)
	}

	out.Computed = len(values)
	if len(values) > 0 {
		sort.Float64s(values)
		out.Median = median(values)
		out.Min = values[0]
		out.Max = values[len(values)-1]
	}
	sort.Strings(out.Missing)
	sort.Strings(out.NeverPaysBack)
	return out
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// Write renders the human-readable report.
func (r *Report) Write(w io.Writer, cfg *Config) {
	if r.Count == 0 {
		fmt.Fprintln(w, "no completed translations found in the given run logs")
		if r.FailedAttempts != nil {
			fmt.Fprintf(w, "  (%d failed attempt(s) recorded, costing %s)\n",
				r.FailedAttempts.Count, formatJoules(r.FailedAttempts.FacilityJoules))
		}
		return
	}

	coeff := DeriveCoefficients(cfg.hardware(), cfg.Models[defaultModelKey], cfg.Serving.Concurrency)
	fmt.Fprintf(w, "Energy model (default model, B=%.0f): e_in %.3f J/token, e_out %.3f J/token\n",
		cfg.Serving.Concurrency, coeff.EIn, coeff.EOut)
	fmt.Fprintf(w, "  prefill %.0f tokens/s, decode step %.1f ms, PUE %.2f\n",
		coeff.PrefillTokensPerSecond, coeff.DecodeStepSeconds*1000, cfg.Facility.PUE)
	// the hardware line is provenance, not decoration: every figure below is
	// linear in it, and a reader must be able to see which serving
	// configuration produced them without opening the config file
	fmt.Fprintf(w, "  hardware: %.0f GPU x %.0f TFLOP/s peak, %.1f TB/s HBM, %.0f W node, %.0f bytes/param\n\n",
		cfg.Hardware.GPUsPerNode, cfg.Hardware.PeakFLOPSPerGPU/1e12,
		cfg.Hardware.HBMBandwidthBytesPerSecond/1e12, cfg.Hardware.NodePowerWatts,
		cfg.Models[defaultModelKey].BytesPerParameter)

	fmt.Fprintf(w, "Translations: %d\n", r.Count)
	fmt.Fprintf(w, "  tokens:        %d prompt / %d output\n", r.TotalPromptTokens, r.TotalEvalTokens)
	fmt.Fprintf(w, "  energy total:  %s\n", formatJoules(r.TotalFacilityJoules))
	r.writeHostEnergy(w)
	fmt.Fprintf(w, "  CO2e:          %.1f g location-based (grid at %.0f g/kWh)\n",
		r.TotalCO2eGrams, cfg.Facility.GridCO2eGramsPerKWh)
	if r.TotalCO2eGramsMarket != nil {
		intensity, _ := cfg.MarketIntensity()
		fmt.Fprintf(w, "                 %.1f g market-based (provider procurement at %.0f g/kWh)\n",
			*r.TotalCO2eGramsMarket, intensity)
	}
	fmt.Fprintf(w, "  per function:  mean %s, median %s\n",
		formatJoules(r.MeanFacilityJoules), formatJoules(r.MedianFacilityJoules))
	fmt.Fprintf(w, "  repair share:  %.1f%% of inference energy\n", r.RepairShare*100)
	fmt.Fprintln(w)

	r.writeFailedAttempts(w)
	r.writeSkipped(w)

	fmt.Fprintln(w, "By stage:")
	fmt.Fprintf(w, "  %-20s %12s %7s %12s %6s %6s %6s\n",
		"task", "inference", "share", "host", "execs", "fails", "calls")
	for _, s := range r.ByStage {
		marker := ""
		if s.IsRepair {
			marker = " (repair)"
		}
		host := "-"
		if s.HostJoules > 0 {
			host = formatCompact(s.HostJoules)
		}
		fmt.Fprintf(w, "  %-20s %12s %6.1f%% %12s %6d %6d %6d%s\n",
			s.Task, formatCompact(s.Joules), s.Share*100, host,
			s.Executions, s.Failures, s.LLMCalls, marker)
	}

	writeGroups(w, "By complexity bucket", r.ByBucket)
	writeGroups(w, "By AWS usage", r.ByAWSUsage)

	if len(r.AssumedModels) > 0 {
		fmt.Fprintf(w, "\nWARNING: costed with default coefficients (no config entry): %s\n",
			strings.Join(r.AssumedModels, ", "))
	}

	if r.BreakEven == nil {
		fmt.Fprintln(w, "\nBreak-even N* not computed: pass -runtime with measured per-invocation")
		fmt.Fprintln(w, "energies (see TODO.md [H6]) to turn these figures into invocation counts.")
		return
	}
	fmt.Fprintf(w, "\nBreak-even N* (invocations until the translation pays for itself):\n")
	fmt.Fprintf(w, "  computed for %d function(s): median %.0f, min %.0f, max %.0f\n",
		r.BreakEven.Computed, r.BreakEven.Median, r.BreakEven.Min, r.BreakEven.Max)
	if len(r.BreakEven.NeverPaysBack) > 0 {
		fmt.Fprintf(w, "  never pays back (Go not faster): %s\n", strings.Join(r.BreakEven.NeverPaysBack, ", "))
	}
	if len(r.BreakEven.Missing) > 0 {
		fmt.Fprintf(w, "  no runtime measurement for: %s\n", strings.Join(r.BreakEven.Missing, ", "))
	}
}

// writeFailedAttempts states what the run spent on jobs that produced nothing.
//
// It is printed above the breakdowns rather than tucked at the end because it
// changes how every figure above it should be read: those describe the
// translations that succeeded, and this is what the same run also paid for.
func (r *Report) writeFailedAttempts(w io.Writer) {
	f := r.FailedAttempts
	if f == nil {
		fmt.Fprintf(w, "Failed attempts:  none - every recorded job produced a translation\n\n")
		return
	}

	fmt.Fprintf(w, "Failed attempts: %d (no translation produced; excluded from every figure above)\n", f.Count)
	fmt.Fprintf(w, "  functions:     %s\n", strings.Join(f.FunctionIDs, ", "))
	fmt.Fprintf(w, "  tokens:        %d prompt / %d output\n", f.PromptTokens, f.EvalTokens)
	fmt.Fprintf(w, "  energy wasted: %s (%.1f g CO2e location-based) - %.1f%% of this run's total spend\n",
		formatJoules(f.FacilityJoules), f.CO2eGrams, f.ShareOfTotalSpend*100)
	fmt.Fprintf(w, "  cost per successful translation, failures amortized in: %s\n\n",
		formatJoules(f.JoulesPerSuccess))
}

// writeSkipped reports what the ex-ante prediction gate declined ([I10]).
//
// It prints nothing at all when no gate was active, so every run log made
// before the gate existed produces a report byte-identical to the one it
// produced before.
func (r *Report) writeSkipped(w io.Writer) {
	s := r.Skipped
	if s == nil {
		return
	}
	fmt.Fprintf(w, "Declined by the prediction gate: %d (never attempted; not counted as failures)\n", s.Count)
	fmt.Fprintf(w, "  functions:     %s\n", strings.Join(s.FunctionIDs, ", "))
	fmt.Fprintf(w, "  energy spent:  %s\n", formatJoules(s.FacilityJoules))
	fmt.Fprintf(w, "  NOTE: these functions were never translated, so this run cannot say whether\n")
	fmt.Fprintf(w, "        they would have succeeded. Run with the gate scoring but not enforcing\n")
	fmt.Fprintf(w, "        to keep that question answerable.\n\n")
}

func writeGroups(w io.Writer, title string, groups []GroupAggregate) {
	if len(groups) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	fmt.Fprintf(w, "  %-14s %6s %12s %8s %8s %10s\n", "group", "count", "mean energy", "passed", "failed", "shape-only")
	for _, g := range groups {
		fmt.Fprintf(w, "  %-14s %6d %12s %8d %8d %10d\n",
			g.Group, g.Count, formatCompact(g.MeanJoules), g.TestsPassed, g.TestsFailed, g.ShapeOnly)
	}
}

// formatJoules renders a headline figure in both the SI unit and the one
// energy is usually quoted in.
func formatJoules(j float64) string {
	switch {
	case j >= joulesPerKWh:
		return fmt.Sprintf("%.2f kWh", j/joulesPerKWh)
	case j >= 1000:
		return fmt.Sprintf("%.1f kJ (%.2f Wh)", j/1000, j/3600)
	default:
		return fmt.Sprintf("%.1f J", j)
	}
}

// formatCompact renders a single unit, for table cells where a dual form
// would break the column alignment.
func formatCompact(j float64) string {
	switch {
	case j >= joulesPerKWh:
		return fmt.Sprintf("%.3f kWh", j/joulesPerKWh)
	case j >= 3600:
		return fmt.Sprintf("%.2f Wh", j/3600)
	default:
		return fmt.Sprintf("%.1f J", j)
	}
}
