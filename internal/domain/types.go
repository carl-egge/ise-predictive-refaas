package domain

import (
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ConversionRequest carries the input package, working copy, metrics, and
// completion state for a conversion run.
type ConversionRequest struct {
	Id             uuid.UUID          `json:"id,omitempty"`
	SourcePackage  *DeploymentPackage `json:"sourcePackage,omitempty"`
	WorkingPackage *DeploymentPackage `json:"workingPackage,omitempty"`
	// Metadata holds auxiliary, non-deployable values produced by
	// metadata-mode LLM tasks (e.g. a summary's "intent"), keyed by the JSON
	// field name the task's prompt was asked to return. Later tasks' prompt
	// templates can reference these directly as top-level vars (see
	// LLMConverter.Apply in internal/translator), e.g. {{ .intent }}.
	Metadata  map[string]string `json:"metadata,omitempty"`
	Metrics   *Metrics          `json:"metrics,omitempty"`
	errs      []error
	Completed bool `json:"completed,omitempty"`
	// CurrentTask is the id of the pipeline task currently executing for
	// this request; maintained by the pipeline and used to attribute LLM
	// calls and chatlogs to their stage. Transient bookkeeping, not part of
	// the JSON shape.
	CurrentTask string `json:"-"`
	// CurrentAttempt is the 1-based execution attempt number of the task
	// currently running for this request (maintained by the pipeline's retry
	// loop); e.g. 2 on the first retry. Lets a converter distinguish a fresh
	// attempt from a resample-style retry - see LLMConverter's opt-in
	// retry_temperature in internal/translator. Transient bookkeeping, not
	// part of the JSON shape.
	CurrentAttempt int `json:"-"`
}

// AddError appends err to the request error list when non-nil.
func (cr *ConversionRequest) AddError(err error) {
	if err == nil {
		return
	}
	cr.errs = append(cr.errs, err)
}

// Errors returns a snapshot of errors collected for the request.
func (cr *ConversionRequest) Errors() []error {
	out := make([]error, len(cr.errs))
	copy(out, cr.errs)
	return out
}

// LastError returns the most recent error for the request, if any.
func (cr *ConversionRequest) LastError() error {
	if len(cr.errs) == 0 {
		return nil
	}
	return cr.errs[len(cr.errs)-1]
}

// DeploymentPackage represents the set of files, tests and build commands for
// a deployment candidate.
type DeploymentPackage struct {
	RootFile   string
	TestFiles  map[string]string
	BuildFiles map[string]string
	BuildCmd   []string
	Env        []string
	Suffix     string
	// Meta is the dataset's per-function metadata (meta.json at the archive
	// root), when the uploaded artifact carried one. Nil for hand-made
	// packages; required only for benchmark runs (see inputhandler.Validate).
	Meta *FunctionMeta
}

// Copy produces a shallow copy of the package maps and slices to be used as a
// recoverable working snapshot.
func (dp *DeploymentPackage) Copy() *DeploymentPackage {
	testCopy := make(map[string]string)
	maps.Copy(testCopy, dp.TestFiles)
	buildFilesCopy := make(map[string]string)
	maps.Copy(buildFilesCopy, dp.BuildFiles)
	cmdCopy := make([]string, len(dp.BuildCmd))
	copy(cmdCopy, dp.BuildCmd)
	envCopy := make([]string, len(dp.Env))
	copy(envCopy, dp.Env)

	return &DeploymentPackage{
		RootFile:   dp.RootFile,
		TestFiles:  testCopy,
		BuildFiles: buildFilesCopy,
		BuildCmd:   cmdCopy,
		Suffix:     dp.Suffix,
		Env:        envCopy,
		// Meta describes the source function and is never mutated by a
		// pipeline stage, so the snapshot shares the pointer deliberately.
		Meta: dp.Meta,
	}
}

// Metrics collects timing and diagnostic information for a conversion run.
type Metrics struct {
	// FunctionID says which dataset element this run translated (see
	// ResolveFunctionID). Without it a metrics dump is a set of anonymous
	// blocks and no per-function result can be computed.
	FunctionID string `json:"function_id,omitempty"`
	// Meta carries the dataset's grouping metadata for that function, so a
	// finished run can be broken down by complexity bucket and AWS usage.
	Meta *FunctionMeta `json:"meta,omitempty"`

	StartTime time.Time
	EndTime   time.Time

	TotalTime time.Duration

	ConversionTime       time.Duration `json:"conversion_time"`
	ConversionPromptTime time.Duration `json:"conversion_prompt_time"`
	ConversionEvalTime   time.Duration `json:"conversion_eval_time"`

	ConversionPromptTokenCount int `json:"conversion_prompt_token_count"`
	ConversionEvalTokenCount   int `json:"conversion_eval_token_count"`

	// Model is the model that produced one LLM call, set by the connector on
	// the per-call Metrics it returns. It is deliberately not aggregated into
	// the request-level Metrics by AddMetric: a pipeline may use a different
	// model per stage, so the meaningful place for it is PerTask below.
	Model string `json:"model,omitempty"`

	BuildTime time.Duration `json:"build_time"`
	TestTime  time.Duration `json:"test_time"`

	BuildError int `json:"build_error"`
	TestError  int `json:"test_error"`
	Tasks      int `json:"tasks"`

	TestCases map[string]bool `json:"test_cases"`
	Issues    []string        `json:"issues"`

	// TestOutcomes records how each test case ended, in execution order.
	// TestCases above is the same information reduced to pass/fail and is
	// kept for existing consumers of /metrics; this is the form the
	// evaluation needs, because a bare "false" cannot distinguish a genuine
	// behavioural divergence from an infrastructure failure - a distinction
	// the dataset's reading guide asks to report separately.
	TestOutcomes []TestOutcome `json:"test_outcomes,omitempty"`

	// PerTask breaks the run down by pipeline task id: attempts, failures,
	// wall-clock time and LLM token spend per stage. This is what makes
	// "which stage exhausts its retries" and "tokens per stage" answerable.
	PerTask map[string]*TaskMetrics `json:"per_task,omitempty"`
}

// TestOutcome is the per-case result the evaluation reads ([H1a]).
//
// The dataset's reading guide interprets each failure kind differently - an
// output mismatch is a real behavioural divergence, a failed side-effect
// assertion says the AWS state diverged, and a setup failure or a packaging
// problem is infrastructure that must be reported apart from translation
// quality. The pipeline already classifies all of these while repairing
// ([C1]); recording the classification here is what stops it being flattened
// back into a bare pass/fail by the time a run is analysed.
type TestOutcome struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	// Kind is one of the TestFailure* constants; empty when the case passed.
	Kind string `json:"kind,omitempty"`
	// OutputMode is the comparison the case was judged under. Cases in
	// "shape" mode compare types only, so they cannot evidence value-level
	// equivalence and the dataset advises excluding them from such claims -
	// which is only possible if the mode is recorded per case.
	OutputMode string `json:"output_mode,omitempty"`
	// Route names the harness that produced this outcome ("goTester" or
	// "flociTester"), since the two validate different things.
	Route string `json:"route,omitempty"`
	// Detail is a short human-readable reason, truncated.
	Detail string `json:"detail,omitempty"`
}

// maxOutcomeDetail keeps a run-log line readable; the full evidence lives in
// TestingError.Failures and the chatlogs.
const maxOutcomeDetail = 500

// RecordTestOutcome appends a per-case result and keeps the legacy
// TestCases map in sync, so existing /metrics consumers keep working.
func (m *Metrics) RecordTestOutcome(o TestOutcome) {
	if len(o.Detail) > maxOutcomeDetail {
		o.Detail = o.Detail[:maxOutcomeDetail] + "... [truncated]"
	}
	m.TestOutcomes = append(m.TestOutcomes, o)
	if m.TestCases == nil {
		m.TestCases = make(map[string]bool)
	}
	m.TestCases[o.Name] = o.Passed
}

// TaskMetrics aggregates one pipeline task's activity across a request.
type TaskMetrics struct {
	Executions   int           `json:"executions"`
	Failures     int           `json:"failures"`
	Duration     time.Duration `json:"duration"`
	LLMCalls     int           `json:"llm_calls"`
	PromptTokens int           `json:"prompt_tokens"`
	EvalTokens   int           `json:"eval_tokens"`
	// Model names the model whose coefficients apply to this stage's tokens.
	// A pipeline may set model_name per task, and energy per token is derived
	// from a specific model's parameter count and weight bytes, so costing a
	// mixed-model run needs this per stage - a run-level average would be
	// quietly wrong (see evaluation/EVALUATION.md).
	//
	// A task compiles its params once, so in practice this is a single name.
	// Should a stage ever see two different models, they are joined with ","
	// rather than silently dropped: that stage's tokens then cannot be costed
	// with one coefficient pair, and the analysis must see that.
	Model string `json:"model,omitempty"`
}

// taskMetrics returns (creating if needed) the per-task entry for id.
func (m *Metrics) taskMetrics(id string) *TaskMetrics {
	if id == "" {
		id = "untracked"
	}
	if m.PerTask == nil {
		m.PerTask = make(map[string]*TaskMetrics)
	}
	tm, ok := m.PerTask[id]
	if !ok {
		tm = &TaskMetrics{}
		m.PerTask[id] = tm
	}
	return tm
}

// RecordTaskAttempt counts one execution attempt of a pipeline task.
func (m *Metrics) RecordTaskAttempt(id string, d time.Duration, success bool) {
	tm := m.taskMetrics(id)
	tm.Executions++
	tm.Duration += d
	if !success {
		tm.Failures++
	}
}

// RecordLLMCall attributes one LLM invocation's token usage - and the model
// that produced it - to a task.
func (m *Metrics) RecordLLMCall(id string, mm Metrics) {
	tm := m.taskMetrics(id)
	tm.LLMCalls++
	tm.PromptTokens += mm.ConversionPromptTokenCount
	tm.EvalTokens += mm.ConversionEvalTokenCount
	tm.recordModel(mm.Model)
}

// recordModel notes which model served a task's call. Normally every call in
// a task uses the same model, so this is a plain assignment; a second,
// different name is appended rather than dropped so the analysis can tell the
// stage apart from a single-model one (see TaskMetrics.Model).
func (tm *TaskMetrics) recordModel(model string) {
	if model == "" || tm.Model == model {
		return
	}
	if tm.Model == "" {
		tm.Model = model
		return
	}
	for _, known := range strings.Split(tm.Model, ",") {
		if known == model {
			return
		}
	}
	tm.Model += "," + model
}

// AddMetric aggregates another Metrics instance into this one.
func (m *Metrics) AddMetric(mm Metrics) {
	m.TotalTime += mm.TotalTime
	m.ConversionTime += mm.ConversionTime
	m.ConversionPromptTime += mm.ConversionPromptTime
	m.ConversionEvalTime += mm.ConversionEvalTime
	m.ConversionPromptTokenCount += mm.ConversionPromptTokenCount
	m.ConversionEvalTokenCount += mm.ConversionEvalTokenCount
	m.BuildTime += mm.BuildTime
	m.BuildError += mm.BuildError
	m.Tasks += mm.Tasks

	// Ignore zero-valued times: connector-returned metrics carry only
	// durations/token counts, and a zero StartTime would otherwise always win
	// the After comparison and reset the request's start to the year 1.
	if !mm.StartTime.IsZero() && (m.StartTime.IsZero() || m.StartTime.After(mm.StartTime)) {
		m.StartTime = mm.StartTime
	}

	if !mm.EndTime.IsZero() && m.EndTime.Before(mm.EndTime) {
		m.EndTime = mm.EndTime
	}
}

// TestFile is the legacy black-box fixture shape (input/output as JSON
// strings). The canonical on-disk schema is internal/fixture.TestCase
// (payload/expectedOutput/outputMode/setup/sideEffects); fixture.Parse lowers
// this legacy shape into it automatically, and this type only remains as the
// definition of that legacy dialect for the lowering.
type TestFile struct {
	Name   string `json:"name"`
	Input  string `json:"input"`
	Output string `json:"output"`
	// Env variables to override in case of a test.
	Env []string `json:"env"`
	// Services to mock/deploy for the test.
	Services map[string]string `json:"services"`
	// UndeterministicResults indicates the test output is non-deterministic.
	UndeterministicResults bool `json:"undeterministic"`
}

// UnmarshalJSON accepts the correctly-named "undeterministic" key and, for
// backwards compatibility with existing fixtures that used the historically
// misnamed tag (e.g. examples/paper/f10 and f14 set "deterministic": true to
// mean "results are non-deterministic"), the legacy "deterministic" key with
// its historical meaning: a true value relaxes output validation.
func (tf *TestFile) UnmarshalJSON(data []byte) error {
	type testFileAlias TestFile
	aux := struct {
		*testFileAlias
		LegacyDeterministic *bool `json:"deterministic"`
	}{testFileAlias: (*testFileAlias)(tf)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if !tf.UndeterministicResults && aux.LegacyDeterministic != nil {
		tf.UndeterministicResults = *aux.LegacyDeterministic
	}
	return nil
}
