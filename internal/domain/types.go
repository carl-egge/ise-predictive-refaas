package domain

import (
	"encoding/json"
	"iter"
	"maps"
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
}

// GetTestFiles yields TestFile entries stored in the package.
func (dp *DeploymentPackage) GetTestFiles() iter.Seq2[*TestFile, error] {
	return func(yield func(*TestFile, error) bool) {
		for name, v := range dp.TestFiles {
			file := &TestFile{}
			err := json.Unmarshal([]byte(v), file)
			file.Name = name
			// Package-level env first, the fixture's own "env" entries last:
			// exec.Cmd keeps the last value for duplicate keys, so per-test
			// overrides win over package defaults instead of being clobbered.
			file.Env = append(append([]string{}, dp.Env...), file.Env...)
			if !yield(file, err) {
				return
			}
		}
	}
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
	}
}

// Metrics collects timing and diagnostic information for a conversion run.
type Metrics struct {
	StartTime time.Time
	EndTime   time.Time

	TotalTime time.Duration

	ConversionTime       time.Duration `json:"conversion_time"`
	ConversionPromptTime time.Duration `json:"conversion_prompt_time"`
	ConversionEvalTime   time.Duration `json:"conversion_eval_time"`

	ConversionPromptTokenCount int `json:"conversion_prompt_token_count"`
	ConversionEvalTokenCount   int `json:"conversion_eval_token_count"`

	BuildTime time.Duration `json:"build_time"`
	TestTime  time.Duration `json:"test_time"`

	BuildError int `json:"build_error"`
	TestError  int `json:"test_error"`
	Tasks      int `json:"tasks"`

	TestCases map[string]bool `json:"test_cases"`
	Issues    []string        `json:"issues"`
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

// TestFile holds the input/output fixtures and environment for a single test
// of the converted function.
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
