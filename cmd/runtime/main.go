// Command runtime measures the Go translation and the Python original of the
// same function under identical conditions, and emits the per-function
// energy figures cmd/energy needs to compute break-even N* ([H6]).
//
// It never runs during a conversion - it is analysis tooling, like cmd/energy
// and cmd/pyscan.
//
//	# measure one function pair
//	go run ./cmd/runtime -artifacts evaluation/evaluation_set -packages runs/packages-<id> -out evaluation/runtime.json
//
//	# then turn the energy report into break-even counts
//	go run ./cmd/energy -runtime evaluation/runtime.json runs/run-*.jsonl
//
// Method (EVALUATION.md §6): both sides run the *same* fixture payloads
// through the *same* envelope (evaluation/harness/), on the same machine, for
// the same number of invocations, under the same energy meter, with the same
// AWS isolation. Cold start is separated from steady state by the two-point
// difference described in measure.go.
//
// What it will not do is invent a joule. If the host has no energy counters
// the run still reports timings - which are real measurements and carry most
// of the signal - and writes no energy figures unless -watts states a package
// power to derive them from, in which case they are tagged as derived
// everywhere they appear.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/builder"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
	"github.com/carl-egge/ise-predictive-refaas/internal/inputhandler"
	"github.com/google/uuid"
)

type options struct {
	artifacts      string
	packages       string
	out            string
	report         string
	meter          string
	watts          float64
	invocations    int
	maxInvocations int
	reps           int
	maxPayloads    int
	only           string
	flociEndpt     string
	flociRegion    string
	keepWork       bool
	noProvision    bool
}

func main() {
	var opt options
	flag.StringVar(&opt.artifacts, "artifacts", "", "directory of original function .zip artifacts (required)")
	flag.StringVar(&opt.packages, "packages", "", "directory of translated Go packages, or a packages-*.zip (required)")
	flag.StringVar(&opt.out, "out", "evaluation/runtime.json", "where to write the cmd/energy runtime file")
	flag.StringVar(&opt.report, "report", "", "optional path for the detailed measurement report (JSON)")
	flag.StringVar(&opt.meter, "meter", "", "energy backend: rapl, perf or time (default: auto-detect)")
	flag.Float64Var(&opt.watts, "watts", 0, "package power for the time backend; without it no joules are reported")
	flag.IntVar(&opt.invocations, "invocations", 200, "invocations in the long run (the N of the two-point split)")
	flag.IntVar(&opt.maxInvocations, "max-invocations", 100000, "cap when escalating N to clear the noise floor")
	flag.IntVar(&opt.reps, "reps", 5, "repetitions per measurement point; the minimum is kept")
	flag.IntVar(&opt.maxPayloads, "max-payloads", 0, "cap the fixture payloads used per function (0 = all)")
	flag.StringVar(&opt.only, "only", "", "comma-separated function ids to measure")
	flag.StringVar(&opt.flociEndpt, "floci-endpoint", "", "AWS endpoint both sides are pinned to (default http://localhost:4566)")
	flag.StringVar(&opt.flociRegion, "floci-region", "", "AWS region both sides run in (default us-east-1)")
	flag.BoolVar(&opt.keepWork, "keep", false, "keep the scratch build directory")
	flag.BoolVar(&opt.noProvision, "no-provision", false,
		"skip emulator provisioning; functions whose fixtures declare setup are then reported as skipped rather than measured against empty state")
	flag.Parse()

	if err := run(opt); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %v\n", err)
		os.Exit(1)
	}
}

// Report is the detailed output; runtime.json is the reduced form cmd/energy
// consumes.
type Report struct {
	Meter          string           `json:"meter"`
	MeterDetail    string           `json:"meter_detail"`
	EnergyMeasured bool             `json:"energy_measured"`
	EnergyDerived  bool             `json:"energy_derived"`
	Invocations    int              `json:"invocations"`
	Repetitions    int              `json:"repetitions"`
	Functions      []FunctionResult `json:"functions"`
	Notes          []string         `json:"notes,omitempty"`
}

// FunctionResult pairs the two sides for one function.
type FunctionResult struct {
	FunctionID string       `json:"function_id"`
	Bucket     string       `json:"bucket,omitempty"`
	AWS        bool         `json:"aws"`
	Python     *Measurement `json:"python,omitempty"`
	Go         *Measurement `json:"go,omitempty"`
	Skipped    string       `json:"skipped,omitempty"`
	// Provisioned records whether this function needed emulator state set up
	// before it could be measured, so the report can separate "no AWS work" from
	// "AWS work against a provisioned emulator".
	Provisioned bool `json:"provisioned,omitempty"`
}

// Measurable reports whether both sides produced comparable energy figures.
func (f FunctionResult) Measurable() bool {
	return f.Skipped == "" && f.Python != nil && f.Go != nil &&
		f.Python.Resolved && f.Go.Resolved &&
		f.Python.HasEnergy && f.Go.HasEnergy
}

func run(opt options) error {
	if opt.artifacts == "" || opt.packages == "" {
		flag.Usage()
		return fmt.Errorf("-artifacts and -packages are required")
	}
	if opt.invocations < 2 {
		return fmt.Errorf("-invocations must be at least 2 to separate startup from steady state")
	}

	python, err := findPython()
	if err != nil {
		return err
	}
	meter, err := NewMeter(opt.meter, opt.watts)
	if err != nil {
		return err
	}

	work, err := os.MkdirTemp("", "refaas-runtime-")
	if err != nil {
		return err
	}
	if opt.keepWork {
		fmt.Fprintf(os.Stderr, "runtime: scratch directory %s\n", work)
	} else {
		defer os.RemoveAll(work)
	}

	harnessPath, err := writeHarness(work)
	if err != nil {
		return err
	}

	translations, err := loadTranslations(opt.packages)
	if err != nil {
		return err
	}
	artifacts, err := filepath.Glob(filepath.Join(opt.artifacts, "*.zip"))
	if err != nil || len(artifacts) == 0 {
		return fmt.Errorf("no .zip artifacts under %s", opt.artifacts)
	}
	sort.Strings(artifacts)

	only := map[string]bool{}
	for _, id := range strings.Split(opt.only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			only[id] = true
		}
	}

	report := &Report{
		Meter:       string(meter.Kind()),
		MeterDetail: meter.Describe(),
		Invocations: opt.invocations,
		Repetitions: opt.reps,
	}
	if meter.Kind() == MeterTime {
		if opt.watts > 0 {
			report.EnergyDerived = true
			report.EnergyMeasured = true
			report.Notes = append(report.Notes, fmt.Sprintf(
				"No energy counters on this host. Energy is DERIVED as %.1f W x measured duration, "+
					"not measured. Every joule below inherits that assumption; the timings do not.", opt.watts))
		} else {
			report.Notes = append(report.Notes,
				"No energy counters on this host and no -watts given, so no joules are reported. "+
					"The timings are real measurements. Re-run on bare-metal Linux with readable "+
					"/sys/class/powercap RAPL counters for measured energy.")
		}
	} else {
		report.EnergyMeasured = true
	}

	// Connect to the emulator up front rather than on first need: 40 of the 95
	// evaluation_set functions declare setup, and discovering the emulator is
	// down after an hour of measuring the other 55 wastes the run.
	var prov *provisioner
	if !opt.noProvision {
		if prov, err = newProvisioner(context.Background(), opt.flociEndpt, opt.flociRegion); err != nil {
			return fmt.Errorf("%w\nstart the Floci emulator (docker compose --profile floci up), "+
				"or pass -no-provision to measure only the functions that need no AWS state", err)
		}
	} else {
		report.Notes = append(report.Notes,
			"Provisioning disabled (-no-provision): functions whose fixtures declare setup were "+
				"skipped rather than measured against empty emulator state.")
	}

	for _, path := range artifacts {
		result := measureFunction(opt, meter, prov, python, harnessPath, work, path, translations, only)
		if result == nil {
			continue
		}
		report.Functions = append(report.Functions, *result)
		logProgress(*result)
	}

	if err := writeRuntimeFile(opt.out, report); err != nil {
		return err
	}
	if opt.report != "" {
		if err := writeJSONFile(opt.report, report); err != nil {
			return err
		}
	}
	printSummary(os.Stderr, report, opt.out)
	return nil
}

func measureFunction(opt options, meter Meter, prov *provisioner, python, harness, work, artifactPath string,
	translations map[string]string, only map[string]bool) *FunctionResult {

	pkg, err := inputhandler.ReadFromFile(artifactPath)
	if err != nil {
		return &FunctionResult{
			FunctionID: strings.TrimSuffix(filepath.Base(artifactPath), ".zip"),
			Skipped:    fmt.Sprintf("unreadable artifact: %v", err),
		}
	}
	id := domain.ResolveFunctionID(pkg.Meta, filepath.Base(artifactPath), uuid.Nil)
	if len(only) > 0 && !only[id] {
		return nil
	}

	result := &FunctionResult{FunctionID: id}
	if pkg.Meta != nil {
		result.Bucket = pkg.Meta.Bucket
		result.AWS = pkg.Meta.AWS
	}

	goSource, ok := translations[id]
	if !ok {
		result.Skipped = "no translated Go package (run the translation first, then point -packages at its output)"
		return result
	}

	cases, err := fixture.FromPackage(pkg)
	if err != nil {
		result.Skipped = fmt.Sprintf("fixtures unreadable: %v", err)
		return result
	}
	payloads := collectPayloads(cases, opt.maxPayloads)
	if len(payloads) == 0 {
		result.Skipped = "no usable fixture payloads"
		return result
	}

	// Provision the AWS state the fixtures expect, before anything is timed.
	// A function whose fixtures declare setup and that is invoked against an
	// empty emulator measures its error path, not its work.
	result.Provisioned = needsProvisioning(cases)
	if result.Provisioned {
		if prov == nil {
			result.Skipped = "fixtures declare setup but provisioning is off; " +
				"start the Floci emulator, or pass -no-provision to measure only the functions that need none"
			return result
		}
		if err := prov.prepare(context.Background(), id, cases); err != nil {
			result.Skipped = err.Error()
			return result
		}
	}

	fnDir := filepath.Join(work, id)
	sourcePath, err := writePythonSource(fnDir, pkg.RootFile)
	if err != nil {
		result.Skipped = err.Error()
		return result
	}
	binary, err := buildGoBinary(filepath.Join(fnDir, "go"), goSource, pkg)
	if err != nil {
		result.Skipped = fmt.Sprintf("go build failed: %v", err)
		return result
	}

	// Both sides get the identical environment, built by the same helper the
	// test stage uses ([C11]): host AWS_* stripped, endpoint and dummy
	// credentials forced. Symmetry here is not cosmetic - an AWS call that
	// resolves differently on the two sides would be measured as a runtime
	// difference.
	env := builder.TestExecutionEnv(hostEnvWithout("AWS_"), pkg.Env, nil, opt.flociEndpt, opt.flociRegion)

	py := pythonRunner(python, harness, sourcePath, env)
	gorun := goRunner(binary, env)

	// Correctness gate before timing: a function that throws on entry is the
	// fastest path through itself, so an unchecked failure would not merely
	// lose a data point, it would bias the comparison toward the failing side.
	if err := checkRuns(py, payloads[0]); err != nil {
		result.Skipped = fmt.Sprintf("python side not runnable: %v", err)
		return result
	}
	if err := checkRuns(gorun, payloads[0]); err != nil {
		result.Skipped = fmt.Sprintf("go side not runnable: %v", err)
		return result
	}

	pyMeasurement, err := measureSide(meter, py, payloads, opt.invocations, opt.reps, opt.maxInvocations)
	if err != nil {
		result.Skipped = err.Error()
		return result
	}
	goMeasurement, err := measureSide(meter, gorun, payloads, opt.invocations, opt.reps, opt.maxInvocations)
	if err != nil {
		result.Skipped = err.Error()
		return result
	}
	result.Python = &pyMeasurement
	result.Go = &goMeasurement
	return result
}

// collectPayloads takes each fixture's payload, compacted onto one line so a
// payload cannot be split across the harnesses' line-oriented input.
func collectPayloads(cases []fixture.TestCase, max int) [][]byte {
	out := make([][]byte, 0, len(cases))
	for _, tc := range cases {
		if len(tc.Payload) == 0 {
			continue
		}
		compact, err := compactJSON(tc.Payload)
		if err != nil {
			continue
		}
		out = append(out, compact)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func compactJSON(raw []byte) ([]byte, error) {
	var buf []byte
	dst := &jsonCompactor{}
	if err := dst.compact(raw); err != nil {
		return nil, err
	}
	buf = dst.out
	return buf, nil
}

type jsonCompactor struct{ out []byte }

func (c *jsonCompactor) compact(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.out = b
	return nil
}

// writeRuntimeFile emits exactly the shape cmd/energy's ReadRuntimeMeasurements
// expects: function id -> {python_joules_per_invocation, go_joules_per_invocation}.
//
// Steady state is what goes in. Break-even asks how many invocations of a
// deployed function repay one translation, and a function invoked N times is
// overwhelmingly warm; charging every invocation a cold start would understate
// N* for both sides at once and flatter the conclusion. The cold figures are
// in the detailed report for the write-up to discuss separately, as §6 asks.
//
// Functions without energy on both sides are omitted rather than written as
// zero: cmd/energy reports a missing function by name, whereas a zero would be
// costed as "free" and silently distort the median N*.
func writeRuntimeFile(path string, report *Report) error {
	out := map[string]map[string]float64{}
	for _, f := range report.Functions {
		if !f.Measurable() {
			continue
		}
		out[f.FunctionID] = map[string]float64{
			"python_joules_per_invocation": f.Python.SteadyJoules,
			"go_joules_per_invocation":     f.Go.SteadyJoules,
		}
	}
	if len(out) == 0 {
		return fmt.Errorf("no function produced energy figures on both sides, so %s was not written "+
			"(see the meter note above; timings are still in -report)", path)
	}
	return writeJSONFile(path, out)
}

func writeJSONFile(path string, v any) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
