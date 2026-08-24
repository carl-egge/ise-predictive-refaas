// Command energy turns the pipeline's run logs into the energy figures of
// evaluation/EVALUATION.md: energy per translation, the per-stage breakdown,
// the repair share, and - given runtime measurements - the break-even
// invocation count N*.
//
// It is analysis tooling and deliberately separate from the service: nothing
// here runs during a conversion, so experimental assumptions cannot leak into
// production behaviour. All constants live in the config file, so replacing an
// assumed value with a measured one is an edit there, not here.
//
// Run logs archive every finished job, including the ones that produced no
// translation. Those are costed but reported apart: every table here describes
// completed translations, and the "Failed attempts" section states what the
// same run spent on the rest, plus the cost per success with failures
// amortized in (TODO.md [H2]).
//
//	go run ./cmd/energy runs/run-*.jsonl
//	go run ./cmd/energy -sweep runs/run-*.jsonl
//	go run ./cmd/energy -runtime evaluation/runtime.json -json runs/run-*.jsonl
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
)

const defaultConfigPath = "evaluation/energy.config.json"

func main() {
	configPath := flag.String("config", defaultConfigPath, "path to the energy constants file")
	runtimePath := flag.String("runtime", "", "optional per-function runtime measurements, to compute N*")
	asJSON := flag.Bool("json", false, "emit the report as JSON instead of text")
	sweep := flag.Bool("sweep", false, "also print the sensitivity table of EVALUATION.md section 8")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: energy [flags] <run-log.jsonl>...")
		flag.PrintDefaults()
		os.Exit(2)
	}

	if err := run(*configPath, *runtimePath, flag.Args(), *asJSON, *sweep); err != nil {
		fmt.Fprintf(os.Stderr, "energy: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath, runtimePath string, logs []string, asJSON, sweep bool) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}

	records, err := ReadRunLogs(logs)
	if err != nil {
		return err
	}

	var runtime map[string]RuntimeMeasurement
	if runtimePath != "" {
		if runtime, err = ReadRuntimeMeasurements(runtimePath); err != nil {
			return err
		}
	}

	jobs := make([]TranslationEnergy, 0, len(records))
	for _, rec := range records {
		jobs = append(jobs, Evaluate(cfg, rec))
	}
	report := Build(cfg, jobs, runtime)

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	report.Write(os.Stdout, cfg)
	if sweep {
		// Completed translations only (report.Translations, not jobs): the
		// table is "mean energy per translation", so its denominator must be
		// the same one the central estimate above it uses.
		WriteSensitivity(os.Stdout, cfg, report.Translations)
	}
	return nil
}

// WriteSensitivity prints the section 8 table: never a single number, always
// the central estimate plus how far each dominant assumption moves it. The
// point of the thesis argument is that the conclusion survives the whole
// plausible range, which can only be shown by sweeping it.
func WriteSensitivity(w *os.File, cfg *Config, translations []TranslationEnergy) {
	if len(translations) == 0 {
		return
	}

	fmt.Fprintf(w, "\nSensitivity (mean facility energy per translation):\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "parameter\tvalue\tmean energy\tvs. central")

	central := meanFacility(cfg, translations)
	fmt.Fprintf(tw, "central estimate\t-\t%s\t1.00x\n", formatCompact(central))

	for _, b := range cfg.Sensitivity.Concurrency {
		variant := *cfg
		variant.Serving.Concurrency = b
		writeSweepRow(tw, "concurrency B", fmt.Sprintf("%.0f", b), &variant, translations, central)
	}
	// Node power and peak FLOP/s are swept because GWDG's 2026-08-22 reply left
	// them open: no monitoring figure was given for node power, and the reply
	// named the precision (FP8) without saying whether the *math* runs in FP8
	// or only the weights are stored that way - which is a factor of two on the
	// prefill peak, and so on e_in.
	for _, p := range cfg.Sensitivity.NodePowerWatts {
		variant := *cfg
		variant.Hardware.NodePowerWatts = p
		writeSweepRow(tw, "node power", fmt.Sprintf("%.0f W", p), &variant, translations, central)
	}
	for _, f := range cfg.Sensitivity.PeakFLOPSPerGPU {
		variant := *cfg
		variant.Hardware.PeakFLOPSPerGPU = f
		writeSweepRow(tw, "prefill peak", fmt.Sprintf("%.0f TFLOP/s", f/1e12), &variant, translations, central)
	}
	for _, bytes := range cfg.Sensitivity.BytesPerParameter {
		variant := *cfg
		variant.Models = scaleModelBytes(cfg.Models, bytes)
		label := "BF16"
		if bytes == 1 {
			label = "FP8"
		}
		writeSweepRow(tw, "precision", label, &variant, translations, central)
	}
	for _, mfu := range cfg.Sensitivity.ModelFLOPUtilization {
		variant := *cfg
		variant.Hardware.ModelFLOPUtilization = mfu
		writeSweepRow(tw, "MFU", fmt.Sprintf("%.2f", mfu), &variant, translations, central)
	}
	for _, pue := range cfg.Sensitivity.PUE {
		variant := *cfg
		variant.Facility.PUE = pue
		writeSweepRow(tw, "PUE", fmt.Sprintf("%.2f", pue), &variant, translations, central)
	}
	tw.Flush()
}

func writeSweepRow(w *tabwriter.Writer, param, value string, cfg *Config, translations []TranslationEnergy, central float64) {
	mean := meanFacility(cfg, translations)
	ratio := 0.0
	if central > 0 {
		ratio = mean / central
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%.2fx\n", param, value, formatCompact(mean), ratio)
}

// meanFacility re-costs the same token counts under a varied configuration.
// Re-costing rather than re-reading is the point: the token counts are
// measured facts, only the coefficients are assumptions.
func meanFacility(cfg *Config, translations []TranslationEnergy) float64 {
	var total float64
	for _, t := range translations {
		total += recost(cfg, t)
	}
	if len(translations) == 0 {
		return 0
	}
	return total / float64(len(translations))
}

func recost(cfg *Config, t TranslationEnergy) float64 {
	var compute float64
	for _, s := range t.Stages {
		model, _ := cfg.ModelFor(s.Model)
		coeff := DeriveCoefficients(cfg.hardware(), model, cfg.Serving.Concurrency)
		compute += float64(s.PromptTokens)*coeff.EIn + float64(s.EvalTokens)*coeff.EOut
	}
	return compute * cfg.Facility.PUE
}

func scaleModelBytes(models map[string]ModelConfig, bytesPerParam float64) map[string]ModelConfig {
	out := make(map[string]ModelConfig, len(models))
	for name, m := range models {
		m.BytesPerParameter = bytesPerParam
		out[name] = m
	}
	return out
}
