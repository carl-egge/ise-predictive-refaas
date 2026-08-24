package pyscan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
)

// requireScanner skips when no interpreter is present. The analysis is a
// hard requirement of the benchmark pipeline but an optional enrichment
// elsewhere, so a dev machine without python3 must not fail the suite.
func requireScanner(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("no python3 interpreter on PATH; set PYSCAN_PYTHON to run these")
	}
}

func scan(t *testing.T, source string) *Result {
	t.Helper()
	requireScanner(t)
	r, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return r
}

const awsSource = `
import json
import boto3
from botocore.exceptions import ClientError

s3 = boto3.client("s3")
table = boto3.resource("dynamodb").Table("things")

def lambda_handler(event, context):
    key = event.get("key")
    if not key or len(key) > 100:
        return {"statusCode": 400, "body": json.dumps({"error": "bad key"})}
    try:
        obj = s3.get_object(Bucket="b", Key=key)
    except ClientError:
        return {"statusCode": 404, "body": "{}"}
    return {"statusCode": 200, "body": obj["Body"].read().decode()}
`

func TestScanExtractsImportsAndServices(t *testing.T) {
	r := scan(t, awsSource)

	if got := r.Metric("n_third_party"); got != 2 {
		t.Errorf("n_third_party = %v, want 2 (boto3, botocore); imports=%v", got, r.ThirdParty)
	}
	want := map[string]bool{"s3": true, "dynamodb": true}
	if len(r.Boto3Services) != 2 {
		t.Fatalf("boto3 services = %v, want s3 and dynamodb", r.Boto3Services)
	}
	for _, svc := range r.Boto3Services {
		if !want[svc] {
			t.Errorf("unexpected boto3 service %q", svc)
		}
	}
	if len(r.TopLevelFuncs) != 1 || r.TopLevelFuncs[0] != "lambda_handler" {
		t.Errorf("top-level functions = %v, want [lambda_handler]", r.TopLevelFuncs)
	}
	// Module-level statements run at import time in Lambda; the imports plus
	// the two client constructions must be visible as such.
	if r.Metric("module_level_stmts") < 2 {
		t.Errorf("module_level_stmts = %v, want at least the two client constructions", r.Metric("module_level_stmts"))
	}
}

func TestScanCountsDecisionPoints(t *testing.T) {
	r := scan(t, awsSource)
	// handler: base 1 + `if` 1 + `or` (BoolOp) 1 + except 1 = 4
	if got := r.Metric("cc"); got != 4 {
		t.Errorf("cc = %v, want 4", got)
	}
	if got := r.Metric("n_except"); got != 1 {
		t.Errorf("n_except = %v, want 1", got)
	}
}

func TestScanDetectsDynamicConstructs(t *testing.T) {
	r := scan(t, `
import functools

def deco(fn):
    @functools.wraps(fn)
    def inner(*args, **kwargs):
        return fn(*args, **kwargs)
    return inner

@deco
def handler(event, context):
    value = getattr(event, "name", None)
    return [x for x in range(10) if x % 2 == 0] + [value]
`)
	if r.Metric("n_decorators") != 2 {
		t.Errorf("n_decorators = %v, want 2", r.Metric("n_decorators"))
	}
	if r.Metric("n_star_args") == 0 || r.Metric("n_kwargs") == 0 {
		t.Errorf("*args/**kwargs not detected: star=%v kwargs=%v", r.Metric("n_star_args"), r.Metric("n_kwargs"))
	}
	if r.DynamicCalls["getattr"] != 1 {
		t.Errorf("getattr not recorded: %v", r.DynamicCalls)
	}
	if r.Metric("n_comprehensions") != 1 {
		t.Errorf("n_comprehensions = %v, want 1", r.Metric("n_comprehensions"))
	}
}

func TestScanRejectsUnparseableSource(t *testing.T) {
	requireScanner(t)
	_, err := Scan(context.Background(), "def broken(:\n    pass\n")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "not parseable") {
		t.Errorf("error should be classified as a parse failure, got: %v", err)
	}
}

func TestScanRejectsEmptySource(t *testing.T) {
	if _, err := Scan(context.Background(), "   \n\t\n"); err == nil {
		t.Fatal("expected an error for empty source")
	}
}

func TestScanHonoursContextCancellation(t *testing.T) {
	requireScanner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, awsSource); err == nil {
		t.Fatal("expected cancellation to abort the scan")
	}
}

// -- feature vector ------------------------------------------------------

func TestBuildFeaturesIsFixedWidthAndOrdered(t *testing.T) {
	r := scan(t, awsSource)
	f, err := BuildFeatures(r, nil)
	if err != nil {
		t.Fatalf("BuildFeatures: %v", err)
	}
	if len(f.Values) != len(featureNames) {
		t.Fatalf("vector width = %d, want %d", len(f.Values), len(featureNames))
	}
	for i, name := range featureNames {
		if f.Names[i] != name {
			t.Fatalf("column %d = %q, want %q - the vector's order is a contract with any trained model", i, f.Names[i], name)
		}
	}
	if f.SchemaVersion != FeatureSchemaVersion {
		t.Errorf("schema version = %d, want %d", f.SchemaVersion, FeatureSchemaVersion)
	}
}

func TestBuildFeaturesEncodesLibrarySurface(t *testing.T) {
	r := scan(t, awsSource)
	f, err := BuildFeatures(r, nil)
	if err != nil {
		t.Fatalf("BuildFeatures: %v", err)
	}
	m := f.Map()
	for name, want := range map[string]float64{
		"lib_boto3":        1,
		"lib_botocore":     1,
		"lib_requests":     0,
		"uses_aws":         1,
		"stdlib_only":      0,
		"n_boto3_services": 2,
		"lib_other":        0,
	} {
		if m[name] != want {
			t.Errorf("%s = %v, want %v", name, m[name], want)
		}
	}
}

func TestBuildFeaturesFlagsInfeasibleLibraries(t *testing.T) {
	r := scan(t, "import numpy\nimport pandas\n\ndef handler(e, c):\n    return numpy.zeros(3).tolist()\n")
	f, err := BuildFeatures(r, nil)
	if err != nil {
		t.Fatalf("BuildFeatures: %v", err)
	}
	if v, _ := f.Value("has_infeasible_lib"); v != 1 {
		t.Error("numpy/pandas must set has_infeasible_lib - this is baseline B4's rule")
	}
	// Outside the closed vocabulary, so they must be counted, not one-hot.
	if v, _ := f.Value("lib_other"); v != 2 {
		t.Errorf("lib_other = %v, want 2", v)
	}
	if w := r.FeasibilityWarning(); !strings.Contains(w, "numpy") || !strings.Contains(w, "pandas") {
		t.Errorf("feasibility warning should name both libraries, got %q", w)
	}
}

func TestBuildFeaturesReadsFixtureSurface(t *testing.T) {
	r := scan(t, awsSource)
	cases := []fixture.TestCase{
		{
			Name:           "t1",
			Payload:        json.RawMessage(`{"key":"a","nested":{"deep":{"x":1}}}`),
			ExpectedOutput: json.RawMessage(`{"statusCode":200,"body":"{}"}`),
		},
		{
			Name:       "t2",
			Payload:    json.RawMessage(`{"key":"b"}`),
			OutputMode: fixture.OutputModeShape,
			Setup:      []fixture.Assertion{{Type: "s3.bucket"}},
			Env:        []string{"REGION=us-east-1"},
		},
		{
			Name:        "t3",
			Payload:     json.RawMessage(`{}`),
			OutputMode:  fixture.OutputModeStrict,
			SideEffects: []fixture.Assertion{{Type: "s3.object"}},
		},
	}
	f, err := BuildFeatures(r, cases)
	if err != nil {
		t.Fatalf("BuildFeatures: %v", err)
	}
	m := f.Map()
	for name, want := range map[string]float64{
		"n_test_cases":              3,
		"n_cases_with_setup":        1,
		"n_cases_with_side_effects": 1,
		"n_cases_with_env":          1,
		"n_mode_tolerant":           1,
		"n_mode_shape":              1,
		"n_mode_strict":             1,
		"n_cases_without_expected":  2,
		"max_payload_depth":         3,
	} {
		if m[name] != want {
			t.Errorf("%s = %v, want %v", name, m[name], want)
		}
	}
}

func TestBuildFeaturesRejectsNilScan(t *testing.T) {
	if _, err := BuildFeatures(nil, nil); err == nil {
		t.Fatal("expected an error for a nil scan")
	}
}

// featureNames and the producers must not drift apart: a name with no
// producer would silently ship a zero column into training.
func TestEveryFeatureHasAProducer(t *testing.T) {
	r := scan(t, "def handler(e, c):\n    return {}\n")
	if _, err := BuildFeatures(r, nil); err != nil {
		t.Fatalf("BuildFeatures on a trivial function: %v", err)
	}
}

func TestFeatureNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range featureNames {
		if seen[n] {
			t.Errorf("duplicate feature column %q", n)
		}
		seen[n] = true
	}
}

// -- prompt hints ---------------------------------------------------------

func TestLibHintsNameGoPackagesAndServices(t *testing.T) {
	r := scan(t, awsSource)
	hints := r.LibHints()
	for _, want := range []string{"boto3 ->", "aws-sdk-go-v2", "encoding/json", "AWS services used: dynamodb, s3"} {
		if !strings.Contains(hints, want) {
			t.Errorf("lib hints missing %q; got:\n%s", want, hints)
		}
	}
}

func TestPyFeaturesListsOnlyWhatIsPresent(t *testing.T) {
	r := scan(t, "def handler(e, c):\n    return {}\n")
	if got := r.PyFeatures(); got != "" {
		t.Errorf("a plain function should produce no construct notes, got:\n%s", got)
	}

	r = scan(t, "def handler(e, c):\n    return [x for x in e]\n")
	if got := r.PyFeatures(); !strings.Contains(got, "comprehension") {
		t.Errorf("comprehension not reported, got:\n%s", got)
	}
}

func TestNoFeasibilityWarningForOrdinaryFunctions(t *testing.T) {
	r := scan(t, awsSource)
	if w := r.FeasibilityWarning(); w != "" {
		t.Errorf("boto3 is translatable; got warning %q", w)
	}
}
