package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// JobRecord is one archived translation from a run log
// (internal/service/runlog.go writes these). Only the fields this tool needs
// are modelled; the record type is deliberately re-declared here rather than
// imported, so the analysis tool depends on the artifact's JSON shape - which
// is what a thesis artifact must stay readable by - and not on service
// internals.
type JobRecord struct {
	Type       string          `json:"type"`
	RunID      string          `json:"run_id"`
	Timestamp  time.Time       `json:"timestamp"`
	JobID      string          `json:"job_id"`
	FunctionID string          `json:"function_id"`
	LLMClient  string          `json:"llm_client"`
	Completed  *bool           `json:"completed"`
	Metrics    *domain.Metrics `json:"metrics"`
}

// IsCompleted reports whether the record describes a job that produced a
// translation.
//
// An absent field means completed. Run logs written before failed jobs were
// archived contain nothing but completed jobs, so decoding a missing
// `completed` into Go's zero value would retroactively reclassify every
// historical translation as a failure - and silently drop those runs out of
// every table in this report. The pointer is what makes "not recorded"
// distinguishable from "recorded as false".
func (r JobRecord) IsCompleted() bool {
	return r.Completed == nil || *r.Completed
}

// recordTypeJob is the run-log line type carrying a finished translation.
const recordTypeJob = "job"

// ReadRunLogs parses every job record from the given JSONL files, in file and
// line order. Non-job lines (the run_start header, reconfigure markers) are
// skipped. A malformed line is reported rather than silently dropped: a run
// log is evidence, and quietly costing fewer translations than actually ran
// would understate the total.
func ReadRunLogs(paths []string) ([]JobRecord, error) {
	var records []JobRecord
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening run log: %w", err)
		}
		scanner := bufio.NewScanner(f)
		// records embed whole prompts' worth of metadata; the default 64 KiB
		// line limit is not enough
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

		line := 0
		for scanner.Scan() {
			line++
			raw := scanner.Bytes()
			if len(raw) == 0 {
				continue
			}
			var rec JobRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				f.Close()
				return nil, fmt.Errorf("%s:%d: malformed run-log line: %w", path, line, err)
			}
			if rec.Type != recordTypeJob {
				continue
			}
			records = append(records, rec)
		}
		if err := scanner.Err(); err != nil {
			f.Close()
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		f.Close()
	}
	return records, nil
}

// RuntimeMeasurement is one function's measured per-invocation energy, as
// produced by the Go-vs-Python comparison ([H6]). Supplying it turns the
// report's energy figures into break-even invocation counts.
type RuntimeMeasurement struct {
	PythonJoulesPerInvocation float64 `json:"python_joules_per_invocation"`
	GoJoulesPerInvocation     float64 `json:"go_joules_per_invocation"`
}

// ReadRuntimeMeasurements loads the optional per-function runtime file, keyed
// by function id.
func ReadRuntimeMeasurements(path string) (map[string]RuntimeMeasurement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading runtime measurements: %w", err)
	}
	var out map[string]RuntimeMeasurement
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parsing runtime measurements %s: %w", path, err)
	}
	return out, nil
}
