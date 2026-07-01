package floci

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// TestCase is a single Floci integration test, loaded from JSON. It extends the
// existing black-box idea (an input payload and an expected output) with
// declarative setup actions that seed AWS state before invocation and
// side-effect assertions that are checked against the emulator afterwards.
type TestCase struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Payload is the raw event passed to the Lambda on invocation.
	Payload json.RawMessage `json:"payload"`

	// ExpectedOutput is matched against the Lambda response using a tolerant
	// JSON-subset comparison (see matchOutput). Omit it to skip output
	// validation and assert only on side effects.
	ExpectedOutput json.RawMessage `json:"expectedOutput,omitempty"`

	// Setup actions run before invocation (e.g. create a bucket/table, seed an
	// item). SideEffects are asserted after invocation. Both are dispatched by
	// their Type through the setup/checker registries, so new kinds can be
	// added without touching the runner.
	Setup       []Assertion `json:"setup,omitempty"`
	SideEffects []Assertion `json:"sideEffects,omitempty"`
}

// Assertion is a single declarative setup action or side-effect check. The Type
// selects the registered handler; Spec carries the full object so the handler
// can decode whatever extra fields it needs (bucket, key, table, ...).
type Assertion struct {
	Type string
	Spec json.RawMessage
}

// UnmarshalJSON captures both the discriminating "type" field and the complete
// raw object, so a handler registered for that type can pull out its own
// parameters.
func (a *Assertion) UnmarshalJSON(data []byte) error {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe.Type == "" {
		return fmt.Errorf("floci: assertion is missing a \"type\" field: %s", string(data))
	}
	a.Type = probe.Type
	a.Spec = append(json.RawMessage(nil), data...)
	return nil
}

// LoadTestCasesFromDir reads every *.json file in dir as a TestCase. Files are
// loaded in lexical order for deterministic runs.
func LoadTestCasesFromDir(dir string) ([]TestCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("floci: reading test case dir %q: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	cases := make([]TestCase, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("floci: reading test case %q: %w", name, err)
		}
		var tc TestCase
		if err := json.Unmarshal(data, &tc); err != nil {
			return nil, fmt.Errorf("floci: parsing test case %q: %w", name, err)
		}
		if tc.Name == "" {
			tc.Name = strings.TrimSuffix(name, ".json")
		}
		cases = append(cases, tc)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("floci: no *.json test cases found in %q", dir)
	}
	return cases, nil
}

// TestCasesFromPackage builds Floci test cases from the test files bundled in a
// DeploymentPackage (the `test/` entries of the uploaded zip). This lets a
// single upload carry both the function and its integration tests, without a
// separate on-disk test_cases_dir.
//
// Each file is interpreted by shape: a file carrying the Floci fields
// (payload / setup / sideEffects) is used as a full, side-effect-aware
// TestCase; anything else is read as the goTester's black-box fixture
// (domain.TestFile: input/output strings) and mapped to a response-only case.
// This keeps the same inputs working for both stages.
func TestCasesFromPackage(pkg *domain.DeploymentPackage) ([]TestCase, error) {
	if pkg == nil {
		return nil, fmt.Errorf("floci: nil deployment package")
	}
	names := make([]string, 0, len(pkg.TestFiles))
	for name := range pkg.TestFiles {
		names = append(names, name)
	}
	sort.Strings(names)

	cases := make([]TestCase, 0, len(names))
	for _, name := range names {
		tc, err := parsePackageTestCase(name, []byte(pkg.TestFiles[name]))
		if err != nil {
			return nil, err
		}
		cases = append(cases, tc)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("floci: package contains no test fixtures")
	}
	return cases, nil
}

// parsePackageTestCase interprets a single bundled test file, preferring the
// rich Floci format and falling back to the black-box fixture format.
func parsePackageTestCase(name string, raw []byte) (TestCase, error) {
	stem := strings.TrimSuffix(filepath.Base(name), ".json")

	var probe struct {
		Payload     json.RawMessage `json:"payload"`
		Setup       json.RawMessage `json:"setup"`
		SideEffects json.RawMessage `json:"sideEffects"`
	}
	_ = json.Unmarshal(raw, &probe) // shape detection only; errors handled below

	if len(probe.Payload) > 0 || len(probe.Setup) > 0 || len(probe.SideEffects) > 0 {
		var tc TestCase
		if err := json.Unmarshal(raw, &tc); err != nil {
			return TestCase{}, fmt.Errorf("floci: parsing bundled test case %q: %w", name, err)
		}
		if tc.Name == "" {
			tc.Name = stem
		}
		return tc, nil
	}

	// Fall back to the goTester fixture format.
	var tf domain.TestFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		return TestCase{}, fmt.Errorf("floci: parsing package test file %q: %w", name, err)
	}
	tcName := tf.Name
	if tcName == "" {
		tcName = stem
	}
	tc := TestCase{
		Name:        tcName,
		Description: "derived from package test fixture",
		Payload:     json.RawMessage(tf.Input),
	}
	if strings.TrimSpace(tf.Output) != "" {
		tc.ExpectedOutput = json.RawMessage(tf.Output)
	}
	return tc, nil
}
