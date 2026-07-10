package fixture

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/compare"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// TestParseLowersLegacyFixtures guards the legacy -> canonical lowering:
// input becomes the payload, output becomes the expected output (omitted
// when empty), undeterministic maps to outputMode "shape", env is carried
// over, and the name defaults to the file stem.
func TestParseLowersLegacyFixtures(t *testing.T) {
	tc, err := Parse("test/t1_f2.json", []byte(`{"input":"{\"num1\": 1}","output":"{\"statusCode\": 200}","env":["EXTRA=1"]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if tc.Name != "t1_f2" {
		t.Errorf("Name = %q, want the file stem", tc.Name)
	}
	if string(tc.Payload) != `{"num1": 1}` {
		t.Errorf("Payload = %s", tc.Payload)
	}
	if string(tc.ExpectedOutput) != `{"statusCode": 200}` {
		t.Errorf("ExpectedOutput = %s", tc.ExpectedOutput)
	}
	if tc.OutputMode != "" || tc.CompareMode() != compare.Tolerant {
		t.Errorf("legacy fixtures should lower to the tolerant default, got %q", tc.OutputMode)
	}
	if !slices.Equal(tc.Env, []string{"EXTRA=1"}) {
		t.Errorf("Env = %v, want the fixture's env carried over", tc.Env)
	}
	if tc.HasSideEffects() {
		t.Error("a lowered black-box fixture must not report side effects")
	}

	// an empty output means "no expectation": output validation is skipped
	tc, err = Parse("test/t2.json", []byte(`{"input":"{}","output":""}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tc.ExpectedOutput) != 0 {
		t.Errorf("empty legacy output must omit ExpectedOutput, got %s", tc.ExpectedOutput)
	}

	// both the correctly named flag and the historical alias select shape
	for _, raw := range []string{
		`{"input":"{}","output":"{\"ok\":true}","undeterministic":true}`,
		`{"input":"{}","output":"{\"ok\":true}","deterministic":true}`,
	} {
		tc, err := Parse("test/t3.json", []byte(raw))
		if err != nil {
			t.Fatalf("Parse(%s): %v", raw, err)
		}
		if tc.OutputMode != "shape" || tc.CompareMode() != compare.ShapeOnly {
			t.Errorf("%s: non-deterministic fixture should lower to outputMode shape, got %q", raw, tc.OutputMode)
		}
	}
}

// TestParseLowersBase64LegacyInput verifies that a legacy fixture whose
// input is base64-encoded JSON (as some externally mined fixtures are) is
// decoded during lowering, while values that are already JSON - or that
// merely look base64-ish but don't decode to JSON - pass through unchanged.
func TestParseLowersBase64LegacyInput(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"num1": 1, "num2": 2}`))
	tc, err := Parse("test/t1.json", []byte(`{"input":"`+encoded+`","output":"{\"ok\":true}"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if string(tc.Payload) != `{"num1": 1, "num2": 2}` {
		t.Errorf("Payload = %s, want the base64-decoded JSON", tc.Payload)
	}

	// plain JSON input stays byte-identical
	tc, err = Parse("test/t2.json", []byte(`{"input":"{\"n\": 3}","output":"{}"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if string(tc.Payload) != `{"n": 3}` {
		t.Errorf("Payload = %s, want the raw JSON untouched", tc.Payload)
	}

	// a non-JSON value that isn't decodable base64 passes through unchanged
	tc, err = Parse("test/t3.json", []byte(`{"input":"not json at all","output":""}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if string(tc.Payload) != "not json at all" {
		t.Errorf("Payload = %s, want the raw value untouched", tc.Payload)
	}
}

// TestParseRichFixturePassthrough verifies the canonical shape parses as-is:
// declared fields land where they should and unknown fields (the external
// dataset's "provenance" block) are tolerated and ignored.
func TestParseRichFixturePassthrough(t *testing.T) {
	raw := []byte(`{
		"name": "happy-path-1",
		"description": "Stores a message in S3.",
		"payload": {"bucket": "audit", "key": "m1.json"},
		"expectedOutput": {"statusCode": 200},
		"outputMode": "strict",
		"setup": [{"type": "s3.bucket", "bucket": "audit"}],
		"sideEffects": [{"type": "s3.objectExists", "bucket": "audit", "key": "m1.json"}],
		"provenance": {"method": "heuristic", "output_source": "golden"}
	}`)
	tc, err := Parse("test/happy.json", raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if tc.Name != "happy-path-1" {
		t.Errorf("Name = %q", tc.Name)
	}
	if tc.CompareMode() != compare.Strict {
		t.Errorf("CompareMode = %v, want Strict", tc.CompareMode())
	}
	if len(tc.Setup) != 1 || tc.Setup[0].Type != "s3.bucket" {
		t.Errorf("Setup = %+v", tc.Setup)
	}
	if len(tc.SideEffects) != 1 || tc.SideEffects[0].Type != "s3.objectExists" {
		t.Errorf("SideEffects = %+v", tc.SideEffects)
	}
	if !tc.HasSideEffects() {
		t.Error("HasSideEffects should report declared setup/sideEffects")
	}
	var payload map[string]string
	if err := json.Unmarshal(tc.Payload, &payload); err != nil || payload["bucket"] != "audit" {
		t.Errorf("Payload = %s (%v)", tc.Payload, err)
	}
}

// TestParseDetectsRichByAnyRichField guards the shape detection: a rich case
// declaring only expectedOutput (or only outputMode) - invoke with the
// default event, check the response - must not be misdetected as a legacy
// fixture, which would silently drop the expectation and pass vacuously.
func TestParseDetectsRichByAnyRichField(t *testing.T) {
	tc, err := Parse("test/out-only.json", []byte(`{"name":"out-only","expectedOutput":{"statusCode":200}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if string(tc.ExpectedOutput) != `{"statusCode":200}` {
		t.Errorf("ExpectedOutput = %s, want it preserved (not lowered away as legacy)", tc.ExpectedOutput)
	}
	if tc.Description == "derived from package test fixture" {
		t.Error("an expectedOutput-only case must be parsed as rich, not lowered")
	}

	tc, err = Parse("test/mode-only.json", []byte(`{"outputMode":"shape"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if tc.CompareMode() != compare.ShapeOnly {
		t.Errorf("CompareMode = %v, want ShapeOnly from the declared outputMode", tc.CompareMode())
	}
}

// TestFromPackageDeterministicOrder verifies package fixtures come out in
// lexical file order regardless of map iteration order.
func TestFromPackageDeterministicOrder(t *testing.T) {
	pkg := &domain.DeploymentPackage{
		TestFiles: map[string]string{
			"test/t3.json": `{"input":"{}","output":""}`,
			"test/t1.json": `{"input":"{}","output":""}`,
			"test/t2.json": `{"payload":{"x":1}}`,
		},
	}
	for i := 0; i < 20; i++ {
		cases, err := FromPackage(pkg)
		if err != nil {
			t.Fatalf("FromPackage: %v", err)
		}
		got := []string{cases[0].Name, cases[1].Name, cases[2].Name}
		if !slices.Equal(got, []string{"t1", "t2", "t3"}) {
			t.Fatalf("cases out of order: %v", got)
		}
	}

	if _, err := FromPackage(&domain.DeploymentPackage{}); err == nil {
		t.Error("a package without fixtures must be an error")
	}
}
