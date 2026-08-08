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

	Count                int     `json:"count"`
	TotalFacilityJoules  float64 `json:"total_facility_joules"`
	MeanFacilityJoules   float64 `json:"mean_facility_joules"`
	MedianFacilityJoules float64 `json:"median_facility_joules"`
	TotalCO2eGrams       float64 `json:"total_co2e_grams"`
	TotalPromptTokens    int     `json:"total_prompt_tokens"`
	TotalEvalTokens      int     `json:"total_eval_tokens"`
	RepairShare          float64 `json:"repair_share"`

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
	Task       string  `json:"task"`
	Joules     float64 `json:"joules"`
	Share      float64 `json:"share"`
	Executions int     `json:"executions"`
	Failures   int     `json:"failures"`
	LLMCalls   int     `json:"llm_calls"`
	IsRepair   bool    `json:"is_repair"`
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
	translations, failed := splitFailed(jobs)

	r := &Report{Translations: translations, Count: len(translations)}
	r.FailedAttempts = summariseFailed(failed)
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

	for _, t := range translations {
		r.TotalFacilityJoules += t.FacilityJoules
		r.TotalCO2eGrams += t.CO2eGrams
		r.TotalPromptTokens += t.PromptTokens
		r.TotalEvalTokens += t.EvalTokens
		repairJoules += t.RepairJoules
		facility = append(facility, t.FacilityJoules)

		for _, s := range t.Stages {
			agg := stages[s.Task]
			if agg == nil {
				agg = &StageAggregate{Task: s.Task, IsRepair: s.IsRepair}
				stages[s.Task] = agg
			}
			agg.Joules += s.Joules
			agg.Executions += s.Executions
			agg.Failures += s.Failures
			agg.LLMCalls += s.LLMCalls
			if s.ModelAssumed {
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
	if r.TotalFacilityJoules > 0 {
		// repair energy is compute-side, so compare like with like
		r.RepairShare = repairJoules / (r.TotalFacilityJoules / cfg.Facility.PUE)
	}

	for _, agg := range stages {
		if compute := r.TotalFacilityJoules / cfg.Facility.PUE; compute > 0 {
			agg.Share = agg.Joules / compute
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

// splitFailed partitions costed jobs into completed translations and failed
// attempts, preserving input order within each.
func splitFailed(jobs []TranslationEnergy) (completed, failed []TranslationEnergy) {
	for _, j := range jobs {
		if j.Completed {
			completed = append(completed, j)
			continue
		}
		failed = append(failed, j)
	}
	return completed, failed
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
	fmt.Fprintf(w, "  prefill %.0f tokens/s, decode step %.1f ms, PUE %.2f\n\n",
		coeff.PrefillTokensPerSecond, coeff.DecodeStepSeconds*1000, cfg.Facility.PUE)

	fmt.Fprintf(w, "Translations: %d\n", r.Count)
	fmt.Fprintf(w, "  tokens:        %d prompt / %d output\n", r.TotalPromptTokens, r.TotalEvalTokens)
	fmt.Fprintf(w, "  energy total:  %s (%.1f g CO2e)\n", formatJoules(r.TotalFacilityJoules), r.TotalCO2eGrams)
	fmt.Fprintf(w, "  per function:  mean %s, median %s\n",
		formatJoules(r.MeanFacilityJoules), formatJoules(r.MedianFacilityJoules))
	fmt.Fprintf(w, "  repair share:  %.1f%% of inference energy\n", r.RepairShare*100)
	fmt.Fprintln(w)

	r.writeFailedAttempts(w)

	fmt.Fprintln(w, "By stage:")
	fmt.Fprintf(w, "  %-20s %12s %7s %6s %6s %6s\n", "task", "energy", "share", "execs", "fails", "calls")
	for _, s := range r.ByStage {
		marker := ""
		if s.IsRepair {
			marker = " (repair)"
		}
		fmt.Fprintf(w, "  %-20s %12s %6.1f%% %6d %6d %6d%s\n",
			s.Task, formatCompact(s.Joules), s.Share*100, s.Executions, s.Failures, s.LLMCalls, marker)
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
	fmt.Fprintf(w, "  energy wasted: %s (%.1f g CO2e) - %.1f%% of this run's total spend\n",
		formatJoules(f.FacilityJoules), f.CO2eGrams, f.ShareOfTotalSpend*100)
	fmt.Fprintf(w, "  cost per successful translation, failures amortized in: %s\n\n",
		formatJoules(f.JoulesPerSuccess))
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
