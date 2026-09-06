package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// noteValidation stamps the report with what decided its contents, and warns
// when nothing did.
//
// The warning is the point. Running without -runlog is not an error - the tool
// still measures what it is pointed at - but the resulting runtime.json can
// hold translations that never passed a test, and that file feeds break-even
// N* straight into the write-up. A figure carries its provenance or it should
// not be quoted, so the absence of a filter is stated as loudly as its
// presence.
func noteValidation(r *Report, v *validation) {
	if v == nil {
		r.ValidatedOnly = false
		r.Notes = append(r.Notes,
			"No -runlog given, so EVERY translated package found was measured, including any that "+
				"failed its fixtures. scripts/run-benchmark.sh archives the package of a failed "+
				"job too, so this output may not be restricted to validated translations. Pass "+
				"-runlog runs/run-<ts>.jsonl to restrict it.")
		return
	}
	r.ValidatedOnly = true
	r.RunLogs = v.runIDs
}

// rebuild regenerates the cmd/energy runtime file from a report produced by an
// earlier measurement run, applying the validation filter without measuring
// anything.
//
// It exists because the filter arrived after the measurements did. Re-running
// the measurement to correct a contaminated runtime.json would take hours and
// would return a fresh sample - different numbers from the ones the rest of
// the analysis was done on - when the correction needed is a subset of rows
// that were already measured correctly.
func rebuild(opt options, v *validation) error {
	data, err := os.ReadFile(opt.fromReport)
	if err != nil {
		return fmt.Errorf("reading -from-report: %w", err)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return fmt.Errorf("parsing -from-report %s: %w", opt.fromReport, err)
	}
	if len(report.Functions) == 0 {
		return fmt.Errorf("%s holds no function results", opt.fromReport)
	}

	before := measurableCount(&report)
	dropped := applyValidation(&report, v)
	noteValidation(&report, v)
	if v != nil {
		report.Notes = append(report.Notes, fmt.Sprintf(
			"Rebuilt from %s without re-measuring: %d of %d measurable functions were dropped as "+
				"unvalidated translations. The figures that remain are the measurements that "+
				"report already held.", opt.fromReport, dropped, before))
	}

	if err := writeRuntimeFile(opt.out, &report); err != nil {
		return err
	}
	if opt.report != "" {
		if err := writeJSONFile(opt.report, &report); err != nil {
			return err
		}
	}
	printSummary(os.Stderr, &report, opt.out)
	return nil
}

// measurableCount is how many functions would reach runtime.json.
func measurableCount(r *Report) int {
	n := 0
	for _, f := range r.Functions {
		if f.Measurable() {
			n++
		}
	}
	return n
}
