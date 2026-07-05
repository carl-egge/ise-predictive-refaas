package floci

import (
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/compare"
)

func TestMatchOutput(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		mode     compare.Mode
		wantErr  bool
	}{
		{"empty expectation always passes", "", `{"anything":1}`, compare.Tolerant, false},
		{"exact json match", `{"status":"ok"}`, `{"status":"ok"}`, compare.Tolerant, false},
		{"subset ignores extra fields", `{"status":"ok"}`, `{"status":"ok","id":"u1","extra":true}`, compare.Tolerant, false},
		{"formatting tolerant", `{"a":1,"b":2}`, "{\n  \"b\": 2,\n  \"a\": 1\n}", compare.Tolerant, false},
		{"nested subset", `{"user":{"name":"Ada"}}`, `{"user":{"name":"Ada","age":36}}`, compare.Tolerant, false},
		{"value mismatch fails", `{"status":"ok"}`, `{"status":"error"}`, compare.Tolerant, true},
		{"missing field fails", `{"id":"u1"}`, `{"status":"ok"}`, compare.Tolerant, true},
		{"array length mismatch fails", `{"xs":[1,2]}`, `{"xs":[1]}`, compare.Tolerant, true},
		{"array match", `{"xs":[1,2]}`, `{"xs":[1,2]}`, compare.Tolerant, false},
		{"non-json substring match", "ok", "result: ok", compare.Tolerant, false},
		{"non-json substring miss", "ok", "result: fail", compare.Tolerant, true},
		// tolerant keeps the historical scalar leniency
		{"tolerant stringified number", `{"n":3}`, `{"n":"3"}`, compare.Tolerant, false},
		// strict rejects it
		{"strict stringified number", `{"n":3}`, `{"n":"3"}`, compare.Strict, true},
		// shape-only: values differ, types match
		{"shape ignores values", `{"temp":21.5}`, `{"temp":3.2}`, compare.ShapeOnly, false},
		{"shape type mismatch", `{"temp":21.5}`, `{"temp":"3.2"}`, compare.ShapeOnly, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := matchOutput([]byte(tt.expected), []byte(tt.actual), tt.mode)
			if (err != nil) != tt.wantErr {
				t.Fatalf("matchOutput(%q,%q,%v) error = %v, wantErr %v", tt.expected, tt.actual, tt.mode, err, tt.wantErr)
			}
		})
	}
}

// TestTestCaseCompareMode verifies the declarative outputMode mapping and
// that black-box fixtures flagged non-deterministic derive shape-only cases.
func TestTestCaseCompareMode(t *testing.T) {
	if (TestCase{}).compareMode() != compare.Tolerant {
		t.Error("default output mode should be tolerant")
	}
	if (TestCase{OutputMode: "strict"}).compareMode() != compare.Strict {
		t.Error("outputMode strict should map to Strict")
	}
	if (TestCase{OutputMode: "shape"}).compareMode() != compare.ShapeOnly {
		t.Error("outputMode shape should map to ShapeOnly")
	}

	tc, err := parsePackageTestCase("test/t1.json", []byte(`{"input":"{}","output":"{\"ok\":true}","undeterministic":true}`))
	if err != nil {
		t.Fatalf("parsePackageTestCase: %v", err)
	}
	if tc.OutputMode != "shape" {
		t.Errorf("non-deterministic fixture should derive OutputMode shape, got %q", tc.OutputMode)
	}
}

func TestScalarsEqual(t *testing.T) {
	if !scalarsEqual(float64(3), float64(3)) {
		t.Error("equal numbers should match")
	}
	if scalarsEqual(float64(3), float64(4)) {
		t.Error("different numbers should not match")
	}
	if !scalarsEqual("x", "x") {
		t.Error("equal strings should match")
	}
	// JSON numbers decode to float64; tolerate comparison against a string form.
	if !scalarsEqual("3", float64(3)) {
		t.Error("string '3' should tolerantly match number 3")
	}
}
