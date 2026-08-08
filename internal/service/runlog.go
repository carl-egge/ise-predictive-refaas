package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	log "github.com/sirupsen/logrus"
)

// Run-log record types.
const (
	runLogRunStart    = "run_start"
	runLogJob         = "job"
	runLogReconfigure = "reconfigure"
)

// defaultRunLogDir is where finished jobs are archived unless RUN_LOG_DIR
// says otherwise. Set RUN_LOG_DIR to "off" (or "") to disable persistence.
const defaultRunLogDir = "runs"

// runLogRecord is one line of the append-only run log. Timestamps are UTC so
// records stay comparable across machines and can be cross-referenced with
// server-side data if the provider supplies any.
type runLogRecord struct {
	Type       string    `json:"type"`
	RunID      string    `json:"run_id"`
	Timestamp  time.Time `json:"timestamp"`
	JobID      string    `json:"job_id,omitempty"`
	FunctionID string    `json:"function_id,omitempty"`
	LLMClient  string    `json:"llm_client,omitempty"`
	// Completed reports whether the conversion produced a translation. It is a
	// pointer so that `false` is written explicitly while the non-job record
	// types (run_start, reconfigure) omit the field entirely - a plain bool
	// with omitempty would drop exactly the value that matters here.
	//
	// Readers must treat an *absent* field as true: run logs written before
	// failed jobs were archived contain completed jobs only, so defaulting to
	// false would retroactively mark every historical translation as a
	// failure.
	Completed *bool           `json:"completed,omitempty"`
	Metrics   *domain.Metrics `json:"metrics,omitempty"`
	Note      string          `json:"note,omitempty"`
}

// runLog appends one JSON object per finished job to runs/<run-id>.jsonl.
//
// Metrics otherwise live only in an in-memory map that a crash, a restart or
// a /reconfigure erases - and a benchmark batch is hours of LLM time and real
// energy spend, so losing it means paying for it twice (TODO.md [H2]). Every
// record is opened, written and closed immediately: a finished job's record
// must survive whatever happens to the next one.
//
// Every finished job is archived, successful or not, tagged with `completed` -
// see recordJob for why the failures belong here too.
//
// The file is created lazily, on the first record, so a service that never
// finishes a job leaves nothing behind.
type runLog struct {
	mu    sync.Mutex
	dir   string
	runID string
	path  string
}

// newRunLog resolves the run-log destination from the environment. A .env
// file is already loaded process-wide via the godotenv autoload import in
// internal/pipeline/defaults.go. Returns nil when persistence is disabled.
func newRunLog() *runLog {
	dir := defaultRunLogDir
	if v, ok := os.LookupEnv("RUN_LOG_DIR"); ok {
		dir = strings.TrimSpace(v)
	}
	if dir == "" || strings.EqualFold(dir, "off") {
		log.Info("run log disabled (RUN_LOG_DIR); job metrics will only be kept in memory")
		return nil
	}

	runID := time.Now().UTC().Format("20060102-150405")
	return &runLog{
		dir:   dir,
		runID: runID,
		path:  filepath.Join(dir, fmt.Sprintf("run-%s.jsonl", runID)),
	}
}

// append writes one record, creating the directory and file on first use and
// prefixing the file with a run_start header so a log always states which run
// it belongs to. Failures are logged and swallowed: losing the archive is bad,
// but failing conversions because a disk is full would be worse.
func (rl *runLog) append(rec runLogRecord) {
	if rl == nil {
		return
	}
	rec.RunID = rl.runID
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if err := os.MkdirAll(rl.dir, 0o755); err != nil {
		log.Errorf("run log: cannot create %s: %v", rl.dir, err)
		return
	}

	_, statErr := os.Stat(rl.path)
	newFile := os.IsNotExist(statErr)

	f, err := os.OpenFile(rl.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Errorf("run log: cannot open %s: %v", rl.path, err)
		return
	}
	defer f.Close()

	if newFile {
		rl.writeLine(f, runLogRecord{
			Type:      runLogRunStart,
			RunID:     rl.runID,
			Timestamp: time.Now().UTC(),
		})
		log.Infof("run log: archiving finished jobs to %s", rl.path)
	}
	rl.writeLine(f, rec)
}

func (rl *runLog) writeLine(f *os.File, rec runLogRecord) {
	data, err := json.Marshal(rec)
	if err != nil {
		log.Errorf("run log: cannot encode %s record: %v", rec.Type, err)
		return
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		log.Errorf("run log: cannot write %s record: %v", rec.Type, err)
	}
}

// recordJob archives a finished conversion, whether or not it produced a
// translation, tagging each record with `completed`.
//
// This used to persist completed jobs only, on the grounds that a failed job
// has no translation to evaluate and mixing the two would distort per-function
// energy and success figures. That reasoning holds for the analysis and is
// where it now lives - cmd/energy reports completed translations by default -
// but it was the wrong place to enforce it, for two reasons the first full
// batch made concrete (TODO.md [H2], run-20260807-132133):
//
//   - A failed attempt still spent its tokens. In that batch the six failed
//     jobs accounted for 86.9 kJ of the run's 127.3 kJ - 68% of the inference
//     energy - and none of it was archived, because failures tend to be the
//     jobs that exhausted their repair budget. An E_translation that silently
//     omits two thirds of the spend is not a measurement.
//   - The fallback did not hold. Failures were said to remain "visible through
//     /metrics", but that map is in-memory and wiped by /reconfigure - the very
//     fragility this run log exists to remove. The data with no durable home
//     was exactly the data the archive excluded.
//
// Discarding is not reversible; labelling is. A tagged record lets the analysis
// filter, and lets a "cost per successful translation" figure amortize the
// attempts that failed.
func (rl *runLog) recordJob(req *domain.ConversionRequest, llmClient string) {
	if rl == nil || req == nil || req.Metrics == nil {
		return
	}

	metrics := *req.Metrics
	completed := req.Completed
	rl.append(runLogRecord{
		Type:       runLogJob,
		JobID:      req.Id.String(),
		FunctionID: metrics.FunctionID,
		LLMClient:  llmClient,
		Completed:  &completed,
		Metrics:    &metrics,
	})
}

// recordReconfigure marks the point where the pipeline configuration changed,
// so records produced under different configurations can be told apart when
// the log is analysed later.
func (rl *runLog) recordReconfigure(llmClient, note string) {
	if rl == nil {
		return
	}
	rl.append(runLogRecord{
		Type:      runLogReconfigure,
		LLMClient: llmClient,
		Note:      note,
	})
}
