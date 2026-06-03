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
	Metrics        *Metrics           `json:"metrics,omitempty"`
	errs           []error
	Completed      bool `json:"completed,omitempty"`
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
			if len(dp.Env) > 0 {
				merged := make([]string, 0, len(dp.Env)+len(file.Env))
				merged = append(merged, dp.Env...)
				merged = append(merged, file.Env...)
				file.Env = merged
			}
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

	if m.StartTime.After(mm.StartTime) {
		m.StartTime = mm.StartTime
	}

	if m.EndTime.Before(mm.EndTime) {
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
	UndeterministicResults bool `json:"deterministic"`
}
