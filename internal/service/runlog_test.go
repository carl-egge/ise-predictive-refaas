package service

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/google/uuid"
)

// testRunLog builds a run log writing into a temp dir.
func testRunLog(t *testing.T) *runLog {
	t.Helper()
	t.Setenv("RUN_LOG_DIR", t.TempDir())
	rl := newRunLog()
	if rl == nil {
		t.Fatal("expected a run log")
	}
	return rl
}

// readRecords returns the decoded lines of the run log, or nil when the file
// was never created.
func readRecords(t *testing.T, rl *runLog) []runLogRecord {
	t.Helper()
	f, err := os.Open(rl.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("opening run log: %v", err)
	}
	defer f.Close()

	var records []runLogRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec runLogRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("run log line is not valid JSON: %v (%s)", err, scanner.Text())
		}
		records = append(records, rec)
	}
	return records
}

func completedJob(functionID string) *domain.ConversionRequest {
	return &domain.ConversionRequest{
		Id:        uuid.New(),
		Completed: true,
		Metrics: &domain.Metrics{
			FunctionID:                 functionID,
			ConversionPromptTokenCount: 1200,
			ConversionEvalTokenCount:   340,
			Meta:                       &domain.FunctionMeta{Bucket: "C", CC: 14, AWS: true},
			PerTask: map[string]*domain.TaskMetrics{
				"convert": {Executions: 1, LLMCalls: 1, PromptTokens: 1200, EvalTokens: 340, Model: "devstral-2-123b"},
			},
			TestOutcomes: []domain.TestOutcome{
				{Name: "t1", Passed: true, OutputMode: "tolerant", Route: "goTester"},
				{Name: "t2", Kind: domain.TestFailureSideEffect, OutputMode: "shape", Route: "flociTester", Detail: "object missing"},
			},
		},
	}
}

// TestRunLogRecordsCompletedJob guards [H2]: a finished translation is
// archived immediately, with the identity and per-stage breakdown the energy
// analysis needs, and the file opens with a run_start header.
func TestRunLogRecordsCompletedJob(t *testing.T) {
	rl := testRunLog(t)
	job := completedJob("f42")

	rl.recordJob(job, "chatai")

	records := readRecords(t, rl)
	if len(records) != 2 {
		t.Fatalf("expected a run_start header plus one job record, got %d: %+v", len(records), records)
	}
	if records[0].Type != runLogRunStart {
		t.Errorf("first line should be the run_start header, got %q", records[0].Type)
	}

	rec := records[1]
	if rec.Type != runLogJob || rec.FunctionID != "f42" {
		t.Errorf("unexpected job record: %+v", rec)
	}
	if rec.JobID != job.Id.String() || rec.LLMClient != "chatai" {
		t.Errorf("job id / client not recorded: %+v", rec)
	}
	if rec.Metrics == nil || rec.Metrics.PerTask["convert"] == nil {
		t.Fatalf("per-stage metrics missing from the archived record: %+v", rec.Metrics)
	}
	if rec.Metrics.PerTask["convert"].PromptTokens != 1200 {
		t.Errorf("token counts not preserved: %+v", rec.Metrics.PerTask["convert"])
	}
	if rec.Metrics.Meta == nil || rec.Metrics.Meta.Bucket != "C" {
		t.Errorf("grouping metadata missing from the archived record: %+v", rec.Metrics.Meta)
	}
	// [H1a]: per-case outcomes must survive into the artifact with their
	// failure kind and comparison mode, or a results table cannot separate a
	// behavioural divergence from an infrastructure failure afterwards.
	if len(rec.Metrics.TestOutcomes) != 2 {
		t.Fatalf("per-test outcomes missing from the archived record: %+v", rec.Metrics.TestOutcomes)
	}
	if got := rec.Metrics.TestOutcomes[1]; got.Kind != domain.TestFailureSideEffect || got.OutputMode != "shape" || got.Route != "flociTester" {
		t.Errorf("outcome classification not preserved: %+v", got)
	}
	if rec.Metrics.PerTask["convert"].Model != "devstral-2-123b" {
		t.Errorf("per-stage model missing from the archived record: %+v", rec.Metrics.PerTask["convert"])
	}
	if rec.Timestamp.IsZero() || rec.Timestamp.Location() != time.UTC {
		t.Errorf("timestamps must be set and in UTC, got %v", rec.Timestamp)
	}
}

// TestRunLogSkipsIncompleteJobs pins the maintainer's rule: only completed
// translations enter the persistent metrics. A failed or cancelled job has no
// translation to evaluate, and mixing it in would distort the per-function
// figures.
func TestRunLogRecordsIncompleteJobsAsFailed(t *testing.T) {
	rl := testRunLog(t)

	failed := completedJob("f7")
	failed.Completed = false
	rl.recordJob(failed, "chatai")

	records := readRecords(t, rl)
	var job *runLogRecord
	for i, rec := range records {
		if rec.Type == runLogJob {
			job = &records[i]
		}
	}
	if job == nil {
		t.Fatalf("a failed job must still be archived - its tokens were spent: %+v", records)
	}
	if job.Completed == nil {
		t.Fatal("completed must be written explicitly, not omitted: an absent field means 'completed' to readers")
	}
	if *job.Completed {
		t.Error("a job that produced no translation must be recorded as completed=false")
	}
	if job.Metrics == nil || job.Metrics.FunctionID != "f7" {
		t.Errorf("a failed job's metrics must survive too: %+v", job.Metrics)
	}
}

// TestRunLogMarksCompletedJobs is the other half: the flag must actually
// distinguish the two, or the analysis cannot filter on it.
func TestRunLogMarksCompletedJobs(t *testing.T) {
	rl := testRunLog(t)
	rl.recordJob(completedJob("f1"), "chatai")

	for _, rec := range readRecords(t, rl) {
		if rec.Type != runLogJob {
			continue
		}
		if rec.Completed == nil || !*rec.Completed {
			t.Errorf("a completed translation must be recorded as completed=true, got %v", rec.Completed)
		}
	}
}

// TestRunLogCompletedFlagOmittedOnNonJobRecords keeps the header and
// reconfigure markers free of a field that only means something for a job.
func TestRunLogCompletedFlagOmittedOnNonJobRecords(t *testing.T) {
	rl := testRunLog(t)
	rl.recordReconfigure("chatai", "switched model")

	for _, rec := range readRecords(t, rl) {
		if rec.Type == runLogJob {
			continue
		}
		if rec.Completed != nil {
			t.Errorf("%s record should carry no completed flag, got %v", rec.Type, *rec.Completed)
		}
	}
}

// TestRunLogAppendsAcrossJobs verifies each record is durable on its own:
// several jobs accumulate, with exactly one header.
func TestRunLogAppendsAcrossJobs(t *testing.T) {
	rl := testRunLog(t)

	rl.recordJob(completedJob("f1"), "ollama")
	rl.recordJob(completedJob("f2"), "ollama")
	rl.recordReconfigure("chatai", "switched config")
	rl.recordJob(completedJob("f3"), "chatai")

	records := readRecords(t, rl)
	if len(records) != 5 {
		t.Fatalf("expected header + 3 jobs + 1 reconfigure, got %d", len(records))
	}
	headers := 0
	for _, rec := range records {
		if rec.Type == runLogRunStart {
			headers++
		}
		if rec.RunID != rl.runID {
			t.Errorf("record not tagged with the run id: %+v", rec)
		}
	}
	if headers != 1 {
		t.Errorf("expected exactly one run_start header, got %d", headers)
	}
	if records[3].Type != runLogReconfigure {
		t.Errorf("reconfigure boundary not recorded in order: %+v", records)
	}
}

func TestRunLogDisabled(t *testing.T) {
	for _, v := range []string{"", "off", "OFF"} {
		t.Setenv("RUN_LOG_DIR", v)
		if rl := newRunLog(); rl != nil {
			t.Errorf("RUN_LOG_DIR=%q should disable the run log", v)
		}
	}
	// a nil run log must be safe to use
	var rl *runLog
	rl.recordJob(completedJob("f1"), "chatai")
	rl.recordReconfigure("chatai", "noop")
}

func TestRunLogPathUsesRunID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RUN_LOG_DIR", dir)
	rl := newRunLog()

	if filepath.Dir(rl.path) != dir {
		t.Errorf("run log path %q not under %q", rl.path, dir)
	}
	base := filepath.Base(rl.path)
	if !strings.HasPrefix(base, "run-") || !strings.HasSuffix(base, ".jsonl") {
		t.Errorf("unexpected run log filename %q", base)
	}
}
