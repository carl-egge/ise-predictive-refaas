// Package fixture defines the canonical on-disk test-fixture schema shared by
// both validation routes (the black-box goTester and the Floci integration
// tester). The rich, side-effect-aware shape (payload / expectedOutput /
// outputMode / setup / sideEffects) is the single source of truth; the legacy
// black-box shape (input / output / undeterministic) is its degenerate case
// and is lowered into it automatically on parse, so existing fixtures keep
// working while new fixtures are authored rich-only.
//
// The field names and the outputMode vocabulary are a contract with the
// external dataset pipeline (which is verified against this package's floci
// counterpart): do not rename fields or modes without coordinating a
// re-vendor on that side. Unknown fields (e.g. a "provenance" block carried
// by externally generated fixtures) are deliberately tolerated and ignored,
// which encoding/json does by default.
package fixture

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/compare"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// TestCase is a single test of a converted function, loaded from JSON. It
// extends the black-box idea (an input payload and an expected output) with
// declarative setup actions that seed AWS state before invocation and
// side-effect assertions that are checked against the emulator afterwards.
// goTester consumes the payload/expectedOutput half; only the Floci stage can
// act on Setup/SideEffects.
type TestCase struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Payload is the raw event passed to the function on invocation.
	Payload json.RawMessage `json:"payload"`

	// ExpectedOutput is matched against the function response using a
	// tolerant JSON-subset comparison (see MatchOutput). Omit it to skip
	// output validation and assert only on side effects.
	ExpectedOutput json.RawMessage `json:"expectedOutput,omitempty"`

	// OutputMode selects how ExpectedOutput is compared: "" or "tolerant"
	// (default: structural subset with tolerant scalars), "strict" (scalar
	// types and values must match exactly), or "shape" (structure and value
	// types only - for non-deterministic outputs like timestamps or
	// generated ids, where value equivalence is impossible by design).
	OutputMode string `json:"outputMode,omitempty"`

	// Setup actions run before invocation (e.g. create a bucket/table, seed an
	// item). SideEffects are asserted after invocation. Both are dispatched by
	// their Type through the floci setup/checker registries, so new kinds can
	// be added without touching the runner. goTester cannot execute them and
	// ignores them with a warning.
	Setup       []Assertion `json:"setup,omitempty"`
	SideEffects []Assertion `json:"sideEffects,omitempty"`

	// Env holds per-test environment overrides ("KEY=value") applied by the
	// local goTester harness on top of the package's own .env entries. Carried
	// over from the legacy fixture shape; the Floci route configures the
	// Lambda environment at deploy time instead.
	Env []string `json:"env,omitempty"`
}

// CompareMode maps the declarative OutputMode onto the shared comparator's
// mode, defaulting to the historical tolerant behavior.
func (tc TestCase) CompareMode() compare.Mode {
	switch tc.OutputMode {
	case "strict":
		return compare.Strict
	case "shape":
		return compare.ShapeOnly
	default:
		return compare.Tolerant
	}
}

// HasSideEffects reports whether the case declares setup actions or
// side-effect assertions - i.e. whether it needs the Floci harness for full
// validation.
func (tc TestCase) HasSideEffects() bool {
	return len(tc.Setup) > 0 || len(tc.SideEffects) > 0
}

// RequiresFloci reports whether a function's fixtures need the Floci harness
// to be validated at all - i.e. whether any case asserts AWS state rather
// than just a response.
//
// This is the classification the pipeline routes on ([C10]): a function whose
// cases only carry payload/expectedOutput is fully validated by the black-box
// goTester, while one that provisions resources or asserts side effects can
// only be validated by deploying it into the emulator. Note that the
// canonical schema declares empty setup/sideEffects arrays for pure
// functions, which HasSideEffects correctly reads as "no side effects".
func RequiresFloci(cases []TestCase) bool {
	for _, tc := range cases {
		if tc.HasSideEffects() {
			return true
		}
	}
	return false
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
		return fmt.Errorf("fixture: assertion is missing a \"type\" field: %s", string(data))
	}
	a.Type = probe.Type
	a.Spec = append(json.RawMessage(nil), data...)
	return nil
}

// LoadFromDir reads every *.json file in dir as a TestCase. Files are loaded
// in lexical order for deterministic runs.
func LoadFromDir(dir string) ([]TestCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("fixture: reading test case dir %q: %w", dir, err)
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
			return nil, fmt.Errorf("fixture: reading test case %q: %w", name, err)
		}
		var tc TestCase
		if err := json.Unmarshal(data, &tc); err != nil {
			return nil, fmt.Errorf("fixture: parsing test case %q: %w", name, err)
		}
		if tc.Name == "" {
			tc.Name = strings.TrimSuffix(name, ".json")
		}
		cases = append(cases, tc)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("fixture: no *.json test cases found in %q", dir)
	}
	return cases, nil
}

// FromPackage builds test cases from the test files bundled in a
// DeploymentPackage (the `test/` entries of the uploaded zip), in lexical
// file order. It fails on the first unparseable fixture; callers that want to
// keep running the remaining cases (goTester) iterate the files themselves
// and call Parse per file.
func FromPackage(pkg *domain.DeploymentPackage) ([]TestCase, error) {
	if pkg == nil {
		return nil, fmt.Errorf("fixture: nil deployment package")
	}
	names := make([]string, 0, len(pkg.TestFiles))
	for name := range pkg.TestFiles {
		names = append(names, name)
	}
	sort.Strings(names)

	cases := make([]TestCase, 0, len(names))
	for _, name := range names {
		tc, err := Parse(name, []byte(pkg.TestFiles[name]))
		if err != nil {
			return nil, err
		}
		cases = append(cases, tc)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("fixture: package contains no test fixtures")
	}
	return cases, nil
}

// Parse interprets a single test file, preferring the rich canonical format
// and lowering the legacy black-box fixture format (domain.TestFile:
// input/output strings) into it: input becomes the payload, output becomes
// the expected output (omitted when empty, which skips output validation),
// and undeterministic: true becomes outputMode "shape". The case name
// defaults to the file stem.
func Parse(name string, raw []byte) (TestCase, error) {
	stem := strings.TrimSuffix(filepath.Base(name), ".json")

	// Shape detection probes every rich-only field name (none of these exist
	// in the legacy dialect): a case declaring only expectedOutput/outputMode
	// (invoke with the default event, check the response) must not be
	// misdetected as legacy, which would silently drop its expectation.
	var probe struct {
		Payload        json.RawMessage `json:"payload"`
		ExpectedOutput json.RawMessage `json:"expectedOutput"`
		OutputMode     string          `json:"outputMode"`
		Setup          json.RawMessage `json:"setup"`
		SideEffects    json.RawMessage `json:"sideEffects"`
	}
	_ = json.Unmarshal(raw, &probe) // shape detection only; errors handled below

	if len(probe.Payload) > 0 || len(probe.ExpectedOutput) > 0 || probe.OutputMode != "" ||
		len(probe.Setup) > 0 || len(probe.SideEffects) > 0 {
		var tc TestCase
		if err := json.Unmarshal(raw, &tc); err != nil {
			return TestCase{}, fmt.Errorf("fixture: parsing test case %q: %w", name, err)
		}
		if tc.Name == "" {
			tc.Name = stem
		}
		return tc, nil
	}

	// Lower the legacy black-box fixture format.
	var tf domain.TestFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		return TestCase{}, fmt.Errorf("fixture: parsing test file %q: %w", name, err)
	}
	tcName := tf.Name
	if tcName == "" {
		tcName = stem
	}
	tc := TestCase{
		Name:        tcName,
		Description: "derived from package test fixture",
		Payload:     json.RawMessage(decodeLegacyValue(tf.Input)),
		Env:         tf.Env,
	}
	if strings.TrimSpace(tf.Output) != "" {
		tc.ExpectedOutput = json.RawMessage(decodeLegacyValue(tf.Output))
	}
	if tf.UndeterministicResults {
		// non-deterministic fixtures are compared by structure and value
		// types only
		tc.OutputMode = "shape"
	}
	return tc, nil
}

// decodeLegacyValue lifts a legacy fixture value into JSON: some externally
// mined legacy fixtures carry their input base64-encoded. When the raw value
// is not itself valid JSON but decodes from base64 to valid JSON, the decoded
// form is used; anything else is passed through unchanged.
func decodeLegacyValue(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" || json.Valid([]byte(trimmed)) {
		return v
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil && json.Valid(bytes.TrimSpace(decoded)) {
		return string(decoded)
	}
	return v
}
