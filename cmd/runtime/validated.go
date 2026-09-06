package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// unvalidatedSkip is the skip reason recorded for a function whose translated
// package exists but never passed its tests. It is a distinct sentence rather
// than a generic "skipped" so the report says why a function that clearly has
// a Go package produced no figure.
const unvalidatedSkip = "not a validated translation: the pipeline did not record this function as completed"

// runLogJob is one archived translation, modelled down to the two fields this
// tool needs. Like cmd/energy's JobRecord it is deliberately re-declared here
// rather than imported from internal/service: the analysis tools depend on the
// run log's JSON shape, which is what a thesis artifact must stay readable by,
// not on service internals.
type runLogJob struct {
	Type       string `json:"type"`
	RunID      string `json:"run_id"`
	FunctionID string `json:"function_id"`
	// Completed is a pointer for the same reason it is one in cmd/energy:
	// run logs written before failed jobs were archived contain nothing but
	// completed jobs, so an absent field means true and only an explicit
	// false is a failure.
	Completed *bool `json:"completed"`
	// Skipped marks a job the ex-ante prediction gate declined ([I10]). Such
	// a job is `completed: false` already, but a log that carries the flag
	// without the field would otherwise read as completed.
	Skipped bool `json:"skipped"`
}

// validation is the set of functions whose translation passed its tests,
// together with where that was read from.
type validation struct {
	ids    map[string]bool
	runIDs []string
	// jobs is how many job records the logs held, so the summary can say
	// whether a near-empty set means "everything failed" or "wrong log".
	jobs int
}

// ok reports whether this function may be measured. A nil validation means no
// run log was supplied and every translation on disk is measured, which is the
// behaviour that produced the contaminated file this exists to prevent - the
// caller warns in that case rather than filtering.
func (v *validation) ok(id string) bool {
	if v == nil {
		return true
	}
	return v.ids[id]
}

// readValidation collects the function ids recorded as completed across the
// given run logs.
//
// The check matters because a translated Go package on disk is not evidence
// that the translation is correct: scripts/run-benchmark.sh archives the
// package of a failed job too (the service returns it with HTTP 406, and it is
// worth keeping as evidence), and cmd/runtime will happily build and time it.
// Measuring a translation that fails its fixtures reports the speed of code
// that computes the wrong answer - and usually reports it as fast, because the
// paths it skips are the work it was supposed to do. That figure then enters
// runtime.json and sets break-even N*.
//
// A function counts as validated if any record for it is completed: a resumed
// run can hold more than one record per function, and the package kept on disk
// is the one whose download succeeded.
func readValidation(paths []string) (*validation, error) {
	v := &validation{ids: map[string]bool{}}
	seen := map[string]bool{}

	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening run log: %w", err)
		}
		scanner := bufio.NewScanner(f)
		// Records embed whole prompts' worth of metadata; the default 64 KiB
		// line limit is not enough.
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

		line := 0
		for scanner.Scan() {
			line++
			raw := scanner.Bytes()
			if len(raw) == 0 {
				continue
			}
			var rec runLogJob
			if err := json.Unmarshal(raw, &rec); err != nil {
				f.Close()
				return nil, fmt.Errorf("%s:%d: malformed run-log line: %w", path, line, err)
			}
			if rec.Type != recordTypeJob || rec.FunctionID == "" {
				continue
			}
			v.jobs++
			if rec.RunID != "" && !seen[rec.RunID] {
				seen[rec.RunID] = true
				v.runIDs = append(v.runIDs, rec.RunID)
			}
			if rec.Skipped || (rec.Completed != nil && !*rec.Completed) {
				continue
			}
			v.ids[rec.FunctionID] = true
		}
		if err := scanner.Err(); err != nil {
			f.Close()
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		f.Close()
	}

	if v.jobs == 0 {
		return nil, fmt.Errorf("no job records in %s; -runlog wants the service's "+
			"runs/run-<ts>.jsonl, not the batch CSV", strings.Join(paths, ", "))
	}
	sort.Strings(v.runIDs)
	return v, nil
}

// recordTypeJob is the run-log line type carrying a finished translation.
const recordTypeJob = "job"

// splitPaths parses the comma-separated -runlog value.
func splitPaths(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// applyValidation marks every unvalidated function in an already-measured
// report as skipped, dropping the measurements with it.
//
// This is what lets a contaminated runtime.json be corrected from the report
// that produced it, instead of by re-measuring: a re-measurement would take
// hours and, being a fresh sample, would not be the same numbers the rest of
// the analysis was done on.
func applyValidation(r *Report, v *validation) int {
	if v == nil {
		return 0
	}
	dropped := 0
	for i := range r.Functions {
		f := &r.Functions[i]
		if f.Skipped != "" || v.ok(f.FunctionID) {
			continue
		}
		f.Skipped = unvalidatedSkip
		f.Python = nil
		f.Go = nil
		f.Validated = false
		dropped++
	}
	return dropped
}
