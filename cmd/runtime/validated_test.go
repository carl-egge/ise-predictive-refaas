package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRunLog writes a JSONL run log with the header line a real one carries,
// so the reader is exercised against the shape it will actually meet.
func writeRunLog(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	body := `{"type":"run_start","run_id":"r1"}` + "\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The contamination this whole path exists to prevent: a package on disk for a
// job the pipeline recorded as failed.
func TestReadValidationExcludesFailedJobs(t *testing.T) {
	log := writeRunLog(t,
		`{"type":"job","run_id":"r1","function_id":"f1","completed":true}`,
		`{"type":"job","run_id":"r1","function_id":"f2","completed":false}`,
	)

	v, err := readValidation([]string{log})
	if err != nil {
		t.Fatal(err)
	}
	if !v.ok("f1") {
		t.Error("f1 completed but was not validated")
	}
	if v.ok("f2") {
		t.Error("f2 failed its fixtures but passed the validation gate")
	}
}

// An absent `completed` means true, exactly as it does in cmd/energy: logs
// written before failed jobs were archived hold completed jobs only, and
// reading the missing field as false would exclude every historical run.
func TestReadValidationTreatsAbsentCompletedAsTrue(t *testing.T) {
	log := writeRunLog(t, `{"type":"job","run_id":"r1","function_id":"f1"}`)

	v, err := readValidation([]string{log})
	if err != nil {
		t.Fatal(err)
	}
	if !v.ok("f1") {
		t.Error("a record without a completed field must count as completed")
	}
}

// A gate-declined job spent no tokens and produced no translation, so it is
// not a validated one either.
func TestReadValidationExcludesGateSkips(t *testing.T) {
	log := writeRunLog(t, `{"type":"job","run_id":"r1","function_id":"f1","skipped":true}`)

	v, err := readValidation([]string{log})
	if err != nil {
		t.Fatal(err)
	}
	if v.ok("f1") {
		t.Error("a gate-declined job must not count as a validated translation")
	}
}

// Pointing -runlog at the batch CSV rather than the JSONL is an easy mistake
// and would otherwise validate nothing at all - silently producing an empty
// runtime.json instead of saying what went wrong.
func TestReadValidationRejectsALogWithNoJobRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch.csv")
	if err := os.WriteFile(path, []byte("function,job_id\nf1,abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := readValidation([]string{path}); err == nil {
		t.Fatal("a file with no job records must be an error, not an empty validation set")
	}
}

// A nil validation is "no -runlog given", which measures everything. The
// warning, not the filter, is what covers that case.
func TestNilValidationAdmitsEverything(t *testing.T) {
	var v *validation
	if !v.ok("anything") {
		t.Error("without a run log every translation must still be measured")
	}
}

func TestNoteValidationWarnsWhenNothingFiltered(t *testing.T) {
	var r Report
	noteValidation(&r, nil)

	if r.ValidatedOnly {
		t.Error("ValidatedOnly must be false when no run log was given")
	}
	if len(r.Notes) == 0 || !strings.Contains(strings.Join(r.Notes, " "), "-runlog") {
		t.Errorf("an unfiltered report must say so in its notes, got %v", r.Notes)
	}
}

func TestNoteValidationRecordsTheRunItFilteredOn(t *testing.T) {
	r := Report{}
	noteValidation(&r, &validation{ids: map[string]bool{"f1": true}, runIDs: []string{"20260904-170428"}, jobs: 1})

	if !r.ValidatedOnly {
		t.Error("ValidatedOnly must be true when a run log decided the contents")
	}
	if len(r.RunLogs) != 1 || r.RunLogs[0] != "20260904-170428" {
		t.Errorf("RunLogs = %v, want the run id the filter came from", r.RunLogs)
	}
}

// applyValidation is the correction path: it must drop the measurements too,
// not merely label them, or Measurable() would still let them through.
func TestApplyValidationDropsUnvalidatedMeasurements(t *testing.T) {
	measured := func() *Measurement {
		return &Measurement{Resolved: true, HasEnergy: true, SteadyJoules: 1}
	}
	r := Report{Functions: []FunctionResult{
		{FunctionID: "f1", Python: measured(), Go: measured()},
		{FunctionID: "f2", Python: measured(), Go: measured()},
	}}

	dropped := applyValidation(&r, &validation{ids: map[string]bool{"f1": true}, jobs: 2})

	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if r.Functions[1].Skipped != unvalidatedSkip {
		t.Errorf("f2 skip reason = %q, want the unvalidated sentinel", r.Functions[1].Skipped)
	}
	if r.Functions[1].Measurable() {
		t.Error("an unvalidated function must not remain measurable")
	}
	if !r.Functions[0].Measurable() {
		t.Error("a validated function must survive the filter")
	}
}

// The end-to-end correction: a report holding a failed translation must
// rebuild into a runtime.json without it, and without re-measuring.
func TestRebuildWritesOnlyValidatedFunctions(t *testing.T) {
	dir := t.TempDir()
	measured := func(j float64) *Measurement {
		return &Measurement{Resolved: true, HasEnergy: true, SteadyJoules: j}
	}
	report := Report{
		Meter:          "rapl",
		EnergyMeasured: true,
		Invocations:    1000,
		Repetitions:    5,
		Functions: []FunctionResult{
			{FunctionID: "f1", Python: measured(2), Go: measured(1)},
			{FunctionID: "f2", Python: measured(4), Go: measured(2)},
		},
	}
	reportPath := filepath.Join(dir, "report.json")
	if err := writeJSONFile(reportPath, &report); err != nil {
		t.Fatal(err)
	}
	log := writeRunLog(t,
		`{"type":"job","run_id":"r1","function_id":"f1","completed":true}`,
		`{"type":"job","run_id":"r1","function_id":"f2","completed":false}`,
	)
	v, err := readValidation([]string{log})
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "runtime.json")
	if err := rebuild(options{fromReport: reportPath, out: out}, v); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]float64
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("runtime.json holds %d functions, want only the validated one: %v", len(got), got)
	}
	if _, ok := got["f1"]; !ok {
		t.Errorf("runtime.json = %v, want f1", got)
	}
	if got["f1"]["go_joules_per_invocation"] != 1 {
		t.Errorf("f1 go joules = %v, want the measurement carried through unchanged",
			got["f1"]["go_joules_per_invocation"])
	}
}

// The summary is where a figure is copied from, so it has to name the
// exclusion rather than fold it into the generic skip count.
func TestPrintSummarySeparatesUnvalidatedFromSkipped(t *testing.T) {
	measured := &Measurement{Resolved: true, HasEnergy: true, SteadySeconds: 1, SteadyJoules: 1}
	r := Report{
		Meter:         "rapl",
		ValidatedOnly: true,
		RunLogs:       []string{"20260904-170428"},
		Functions: []FunctionResult{
			{FunctionID: "f1", Python: measured, Go: measured},
			{FunctionID: "f2", Skipped: unvalidatedSkip},
			{FunctionID: "f3", Skipped: "no usable fixture payloads"},
		},
	}

	var buf bytes.Buffer
	printSummary(&buf, &r, "runtime.json")
	out := buf.String()

	if !strings.Contains(out, "unvalidated: 1") {
		t.Errorf("summary must count the unvalidated exclusion separately:\n%s", out)
	}
	if !strings.Contains(out, "skipped:     1") {
		t.Errorf("summary must still count the genuine skip:\n%s", out)
	}
	if !strings.Contains(out, "20260904-170428") {
		t.Errorf("summary must name the run the filter came from:\n%s", out)
	}
}

func TestPrintSummaryFlagsAnUnfilteredRun(t *testing.T) {
	measured := &Measurement{Resolved: true, HasEnergy: true, SteadySeconds: 1, SteadyJoules: 1}
	r := Report{Meter: "rapl", Functions: []FunctionResult{{FunctionID: "f1", Python: measured, Go: measured}}}

	var buf bytes.Buffer
	printSummary(&buf, &r, "runtime.json")

	if !strings.Contains(buf.String(), "validated:   NO") {
		t.Errorf("a run without -runlog must say so where the meter is reported:\n%s", buf.String())
	}
}
