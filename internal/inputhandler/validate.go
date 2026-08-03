package inputhandler

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
)

// ValidateOptions controls how strictly an uploaded package is checked.
type ValidateOptions struct {
	// RequireMeta rejects packages without a meta.json. Enabled for benchmark
	// runs, where every artifact carries one by construction and a missing
	// file means a mispackaged upload whose result could not be attributed to
	// a dataset element afterwards. Off by default so ad-hoc uploads, the
	// bundled examples/input/*.zip and the README's curl example keep working.
	RequireMeta bool
}

// BenchmarkValidateOptions reads the upload-admission policy from the
// environment: REQUIRE_META=true|1 turns on the benchmark-mode checks.
//
// This is deliberately not part of pipeline.envDefaults, which exists for
// connector configuration that must be re-read on every /reconfigure; an
// upload policy is neither connector config nor per-conversion state. A .env
// file is already loaded process-wide by the godotenv autoload import in
// internal/pipeline/defaults.go.
func BenchmarkValidateOptions() ValidateOptions {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("REQUIRE_META")))
	return ValidateOptions{RequireMeta: v == "true" || v == "1"}
}

// ValidationError reports everything wrong with an uploaded package at once,
// so a client fixing a bad artifact does not have to discover the problems
// one upload at a time.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	return fmt.Sprintf("%d problems: %s", len(e.Problems), strings.Join(e.Problems, "; "))
}

// Validate checks an uploaded package before the pipeline spends any LLM or
// build budget on it: a package with no source file, no fixtures, or
// unparseable fixtures cannot produce a meaningful result, and with zero
// fixtures the test stage would pass vacuously and report a "success" that
// validates nothing.
func Validate(dp *domain.DeploymentPackage, opts ValidateOptions) error {
	var problems []string

	if dp == nil {
		return &ValidationError{Problems: []string{"the archive could not be read"}}
	}

	if strings.TrimSpace(dp.RootFile) == "" {
		problems = append(problems, "no source file: the archive must contain exactly one .py (or .go) file at its root")
	}

	if len(dp.TestFiles) == 0 {
		problems = append(problems, "no test fixtures: the archive must contain at least one test/*.json file, otherwise the translation cannot be validated")
	}

	// Parse every fixture through the canonical schema, so a malformed one is
	// reported here rather than at comparison time, several stages later.
	names := make([]string, 0, len(dp.TestFiles))
	for name := range dp.TestFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := fixture.Parse(name, []byte(dp.TestFiles[name])); err != nil {
			problems = append(problems, fmt.Sprintf("invalid test fixture %s: %v", name, err))
		}
	}

	if opts.RequireMeta && dp.Meta == nil {
		problems = append(problems, fmt.Sprintf(
			"missing %s: benchmark runs require the dataset's per-function metadata, without which the result cannot be attributed to a dataset element",
			domain.MetaFileName))
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}
