package domain

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// TestParseFunctionMeta covers the dataset's documented meta.json fields plus
// the two robustness rules: invalid JSON is an error (a mispackaged artifact),
// while a field whose type differs from what we model is not - the typed
// fields degrade but Raw still carries everything.
func TestParseFunctionMeta(t *testing.T) {
	raw := []byte(`{
		"bucket": "C",
		"cc": 14,
		"lloc": 87,
		"type": "aws",
		"aws": true,
		"imports": ["boto3", "json"],
		"description": "stores a message in S3",
		"provenance": {"method": "llm"},
		"future_field": 42
	}`)

	meta, err := ParseFunctionMeta(raw)
	if err != nil {
		t.Fatalf("ParseFunctionMeta: %v", err)
	}
	if meta.Bucket != "C" || meta.CC != 14 || meta.LLOC != 87 || !meta.AWS {
		t.Errorf("grouping fields not parsed: %+v", meta)
	}
	if len(meta.Imports) != 2 || meta.Description == "" {
		t.Errorf("imports/description not parsed: %+v", meta)
	}
	if len(meta.Provenance) == 0 {
		t.Error("provenance should be preserved")
	}
	// a field this struct does not model must survive via Raw
	var round map[string]interface{}
	if err := json.Unmarshal(meta.Raw, &round); err != nil {
		t.Fatalf("Raw is not valid JSON: %v", err)
	}
	if round["future_field"] != float64(42) {
		t.Errorf("unmodelled field lost from Raw: %v", round)
	}

	// the whole struct must stay marshalable, since it ends up in /metrics
	if _, err := json.Marshal(meta); err != nil {
		t.Fatalf("FunctionMeta must be marshalable: %v", err)
	}
}

func TestParseFunctionMetaRejectsNonObject(t *testing.T) {
	for _, raw := range []string{"", "not json", "[1,2,3]", `"a string"`} {
		if _, err := ParseFunctionMeta([]byte(raw)); err == nil {
			t.Errorf("expected an error for %q", raw)
		}
	}
}

func TestParseFunctionMetaToleratesUnexpectedTypes(t *testing.T) {
	// "aws" as a string rather than a bool: the dataset owns this schema, so
	// a guessed-wrong type must not fail the upload.
	raw := []byte(`{"aws": "yes", "bucket": "A"}`)
	meta, err := ParseFunctionMeta(raw)
	if err != nil {
		t.Fatalf("unexpected field types must not error: %v", err)
	}
	if len(meta.Raw) == 0 {
		t.Error("Raw must still carry the original bytes")
	}
}

func TestFunctionStem(t *testing.T) {
	cases := map[string]string{
		"f42.zip":                       "f42",
		"evaluation_set/f42.zip":        "f42",
		`C:\datasets\evaluation\f7.zip`: "f7",
		"main":                          "main",
		"":                              "",
		"/":                             "",
	}
	for in, want := range cases {
		if got := FunctionStem(in); got != want {
			t.Errorf("FunctionStem(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResolveFunctionID pins the precedence: an explicit id from meta.json
// wins, then the artifact's filename stem (the dataset's own convention),
// then a short job-uuid form so a record is never unattributable.
func TestResolveFunctionID(t *testing.T) {
	id := uuid.New()

	if got := ResolveFunctionID(&FunctionMeta{Name: "store-message"}, "f42.zip", id); got != "store-message" {
		t.Errorf("meta name should win, got %q", got)
	}
	if got := ResolveFunctionID(&FunctionMeta{ID: "f99"}, "f42.zip", id); got != "f99" {
		t.Errorf("meta id should win over the stem, got %q", got)
	}
	if got := ResolveFunctionID(nil, "f42.zip", id); got != "f42" {
		t.Errorf("stem should be used without meta, got %q", got)
	}
	if got := ResolveFunctionID(&FunctionMeta{}, "f42.zip", id); got != "f42" {
		t.Errorf("empty meta should fall through to the stem, got %q", got)
	}
	got := ResolveFunctionID(nil, "", id)
	if got != "job-"+id.String()[:8] {
		t.Errorf("expected a job-uuid fallback, got %q", got)
	}
}
