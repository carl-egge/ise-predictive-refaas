package pyscan

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
)

// FeatureSchemaVersion identifies the vector's width and column order. A
// trained model is only valid for the version it was fitted on, so the
// exported model records this and scoring refuses a mismatch rather than
// silently feeding a reordered vector through stale coefficients.
//
// Bump on ANY change to featureNames: added column, removed column, reorder.
const FeatureSchemaVersion = 1

// Feature families, in the order [I3] defines them. Every value is derived
// from the uploaded artifact alone - Python source plus fixtures - and none
// of them requires an LLM call or anything the pipeline only learns after
// translation has started.
//
// Leakage rule: no column here may depend on a compiler diagnostic, a token
// count, a stage duration or a test outcome. Adding such a column would make
// cross-validated scores meaningless, since none of it is knowable at the
// moment the gate has to decide.
var featureNames = []string{
	// -- 1. size and complexity -------------------------------------
	"lloc",
	"cc",
	"cc_total",
	"cc_per_lloc",
	"max_nesting_depth",
	"n_defs",
	"n_classes",
	"n_branches",
	"n_loops",
	"source_lines",
	"halstead_difficulty",
	"halstead_vocabulary",
	"halstead_length",

	// -- 2. library surface ------------------------------------------
	"n_imports",
	"n_third_party",
	"n_boto3_services",
	"uses_aws",
	"stdlib_only",
	"has_infeasible_lib",
	"n_unmapped_third_party",
	"lib_boto3",
	"lib_botocore",
	"lib_requests",
	"lib_urllib3",
	"lib_dateutil",
	"lib_bs4",
	"lib_yaml",
	"lib_pytz",
	"lib_jwt",
	"lib_other",

	// -- 3. dynamic-Python markers -----------------------------------
	"n_dynamic_calls",
	"uses_eval_exec",
	"uses_reflection",
	"n_decorators",
	"n_star_args",
	"n_kwargs",
	"n_yield",
	"n_async",
	"n_comprehensions",
	"n_try",
	"n_except",
	"n_raise",
	"n_lambdas",
	"n_fstrings",
	"module_level_stmts",

	// -- 4. fixture surface (free; read straight off the upload) -----
	"n_test_cases",
	"mean_payload_bytes",
	"max_payload_depth",
	"n_cases_with_setup",
	"n_cases_with_side_effects",
	"n_mode_tolerant",
	"n_mode_strict",
	"n_mode_shape",
	"n_cases_without_expected",
	"mean_expected_fields",
	"n_cases_with_env",
}

// FeatureNames returns the vector's column names in their fixed order.
func FeatureNames() []string {
	out := make([]string, len(featureNames))
	copy(out, featureNames)
	return out
}

// Features is one function's feature vector: a fixed-order numeric slice
// plus the names, so a caller can emit a CSV header or a labelled map
// without re-deriving the order.
type Features struct {
	SchemaVersion int       `json:"schema_version"`
	Names         []string  `json:"names"`
	Values        []float64 `json:"values"`
}

// Value returns one named feature, and whether it exists.
func (f Features) Value(name string) (float64, bool) {
	for i, n := range f.Names {
		if n == name {
			return f.Values[i], true
		}
	}
	return 0, false
}

// Map renders the vector as a name->value map, for JSON output and tests.
func (f Features) Map() map[string]float64 {
	out := make(map[string]float64, len(f.Names))
	for i, n := range f.Names {
		out[n] = f.Values[i]
	}
	return out
}

// BuildFeatures assembles the vector from a source scan and the function's
// fixtures. cases may be empty - an upload always has fixtures ([C6]
// enforces it), but the scanner is also useful on a bare source file, and a
// zero fixture family is more useful than a hard failure.
func BuildFeatures(r *Result, cases []fixture.TestCase) (Features, error) {
	if r == nil {
		return Features{}, fmt.Errorf("pyscan: cannot build features from a nil scan")
	}

	src := sourceFeatures(r)
	fx := fixtureFeatures(cases)

	values := make([]float64, len(featureNames))
	for i, name := range featureNames {
		if v, ok := src[name]; ok {
			values[i] = v
			continue
		}
		if v, ok := fx[name]; ok {
			values[i] = v
			continue
		}
		// A name in featureNames with no producer is a programming error,
		// not a data condition: it would silently ship a zero column into
		// training. Fail loudly instead.
		return Features{}, fmt.Errorf("pyscan: feature %q has no producer", name)
	}

	return Features{
		SchemaVersion: FeatureSchemaVersion,
		Names:         FeatureNames(),
		Values:        values,
	}, nil
}

// sourceFeatures derives families 1-3 from the Python scan.
func sourceFeatures(r *Result) map[string]float64 {
	imports := make(map[string]bool, len(r.Imports))
	for _, m := range r.Imports {
		imports[m] = true
	}

	dynamicTotal := 0
	evalExec := 0
	reflection := 0
	for name, n := range r.DynamicCalls {
		dynamicTotal += n
		switch name {
		case "eval", "exec", "compile", "__import__":
			evalExec += n
		case "getattr", "setattr", "delattr", "hasattr", "globals", "locals", "vars":
			reflection += n
		}
	}

	// Third-party imports we have no mapping for are the ones the model must
	// reason about unaided - a different and more informative signal than the
	// raw third-party count.
	unmapped := 0
	for _, m := range r.ThirdParty {
		if _, known := libMappings[m]; !known {
			unmapped++
		}
	}

	inVocab := 0
	f := map[string]float64{
		"lloc":                r.Metric("lloc"),
		"cc":                  r.Metric("cc"),
		"cc_total":            r.Metric("cc_total"),
		"cc_per_lloc":         ratio(r.Metric("cc"), r.Metric("lloc")),
		"max_nesting_depth":   r.Metric("max_nesting_depth"),
		"n_defs":              r.Metric("n_defs"),
		"n_classes":           r.Metric("n_classes"),
		"n_branches":          r.Metric("n_branches"),
		"n_loops":             r.Metric("n_loops"),
		"source_lines":        r.Metric("source_lines"),
		"halstead_difficulty": r.Metric("halstead_difficulty"),
		"halstead_vocabulary": r.Metric("halstead_vocabulary"),
		"halstead_length":     r.Metric("halstead_length"),

		"n_imports":              r.Metric("n_imports"),
		"n_third_party":          r.Metric("n_third_party"),
		"n_boto3_services":       float64(len(r.Boto3Services)),
		"uses_aws":               boolFeature(r.UsesAWS()),
		"stdlib_only":            boolFeature(len(r.ThirdParty) == 0),
		"has_infeasible_lib":     boolFeature(len(Infeasible(r.Imports)) > 0),
		"n_unmapped_third_party": float64(unmapped),

		"n_dynamic_calls":    float64(dynamicTotal),
		"uses_eval_exec":     boolFeature(evalExec > 0),
		"uses_reflection":    boolFeature(reflection > 0),
		"n_decorators":       r.Metric("n_decorators"),
		"n_star_args":        r.Metric("n_star_args"),
		"n_kwargs":           r.Metric("n_kwargs"),
		"n_yield":            r.Metric("n_yield"),
		"n_async":            r.Metric("n_async_defs") + r.Metric("n_await"),
		"n_comprehensions":   r.Metric("n_comprehensions"),
		"n_try":              r.Metric("n_try"),
		"n_except":           r.Metric("n_except"),
		"n_raise":            r.Metric("n_raise"),
		"n_lambdas":          r.Metric("n_lambdas"),
		"n_fstrings":         r.Metric("n_fstrings"),
		"module_level_stmts": r.Metric("module_level_stmts"),
	}

	for _, lib := range vocabulary {
		present := imports[lib]
		f["lib_"+lib] = boolFeature(present)
		if present {
			inVocab++
		}
	}
	// Third-party imports outside the closed vocabulary, counted rather than
	// one-hot encoded so a rare library cannot add a column.
	other := 0
	for _, m := range r.ThirdParty {
		if !inVocabulary(m) {
			other++
		}
	}
	f["lib_other"] = float64(other)

	return f
}

// fixtureFeatures derives family 4 from the function's own test fixtures.
// These cost nothing - the fixtures are already parsed at upload - and they
// describe the validation surface a translation has to satisfy, which is a
// difficulty signal independent of the source.
func fixtureFeatures(cases []fixture.TestCase) map[string]float64 {
	f := map[string]float64{
		"n_test_cases":              float64(len(cases)),
		"mean_payload_bytes":        0,
		"max_payload_depth":         0,
		"n_cases_with_setup":        0,
		"n_cases_with_side_effects": 0,
		"n_mode_tolerant":           0,
		"n_mode_strict":             0,
		"n_mode_shape":              0,
		"n_cases_without_expected":  0,
		"mean_expected_fields":      0,
		"n_cases_with_env":          0,
	}
	if len(cases) == 0 {
		return f
	}

	totalPayload := 0
	totalExpectedFields := 0
	maxDepth := 0

	for _, tc := range cases {
		totalPayload += len(tc.Payload)
		if d := jsonDepth(tc.Payload); d > maxDepth {
			maxDepth = d
		}
		if len(tc.Setup) > 0 {
			f["n_cases_with_setup"]++
		}
		if len(tc.SideEffects) > 0 {
			f["n_cases_with_side_effects"]++
		}
		if len(tc.Env) > 0 {
			f["n_cases_with_env"]++
		}
		switch tc.OutputModeName() {
		case fixture.OutputModeStrict:
			f["n_mode_strict"]++
		case fixture.OutputModeShape:
			f["n_mode_shape"]++
		default:
			f["n_mode_tolerant"]++
		}
		if len(tc.ExpectedOutput) == 0 {
			f["n_cases_without_expected"]++
			continue
		}
		totalExpectedFields += topLevelFields(tc.ExpectedOutput)
	}

	n := float64(len(cases))
	f["mean_payload_bytes"] = float64(totalPayload) / n
	f["max_payload_depth"] = float64(maxDepth)
	f["mean_expected_fields"] = float64(totalExpectedFields) / n
	return f
}

func inVocabulary(module string) bool {
	for _, v := range vocabulary {
		if v == module {
			return true
		}
	}
	return false
}

func boolFeature(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// ratio guards the zero denominator; an empty file has no complexity density.
func ratio(num, den float64) float64 {
	if den == 0 || math.IsNaN(den) {
		return 0
	}
	return num / den
}

// jsonDepth reports the nesting depth of a JSON document, 0 for a scalar or
// unparseable input. Deeply nested event payloads are harder to model as Go
// structs than flat ones, which is what this is measuring.
func jsonDepth(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0
	}
	return depth(v)
}

func depth(v any) int {
	switch t := v.(type) {
	case map[string]any:
		max := 0
		for _, child := range t {
			if d := depth(child); d > max {
				max = d
			}
		}
		return max + 1
	case []any:
		max := 0
		for _, child := range t {
			if d := depth(child); d > max {
				max = d
			}
		}
		return max + 1
	default:
		return 0
	}
}

// topLevelFields counts the keys of a JSON object, 0 for anything else.
func topLevelFields(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return 0
	}
	return len(obj)
}

// CSVHeader returns the feature names as a CSV header line, used by the
// offline dataset builder ([I4]) so the table's column order is produced by
// this package rather than restated there.
func CSVHeader() string {
	return strings.Join(featureNames, ",")
}

// SortedNames returns the column names alphabetically, for reporting only.
// The vector's own order is featureNames and must not be sorted.
func SortedNames() []string {
	out := FeatureNames()
	sort.Strings(out)
	return out
}
