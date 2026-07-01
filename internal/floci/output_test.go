package floci

import "testing"

func TestMatchOutput(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		wantErr  bool
	}{
		{"empty expectation always passes", "", `{"anything":1}`, false},
		{"exact json match", `{"status":"ok"}`, `{"status":"ok"}`, false},
		{"subset ignores extra fields", `{"status":"ok"}`, `{"status":"ok","id":"u1","extra":true}`, false},
		{"formatting tolerant", `{"a":1,"b":2}`, "{\n  \"b\": 2,\n  \"a\": 1\n}", false},
		{"nested subset", `{"user":{"name":"Ada"}}`, `{"user":{"name":"Ada","age":36}}`, false},
		{"value mismatch fails", `{"status":"ok"}`, `{"status":"error"}`, true},
		{"missing field fails", `{"id":"u1"}`, `{"status":"ok"}`, true},
		{"array length mismatch fails", `{"xs":[1,2]}`, `{"xs":[1]}`, true},
		{"array match", `{"xs":[1,2]}`, `{"xs":[1,2]}`, false},
		{"non-json substring match", "ok", "result: ok", false},
		{"non-json substring miss", "ok", "result: fail", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := matchOutput([]byte(tt.expected), []byte(tt.actual))
			if (err != nil) != tt.wantErr {
				t.Fatalf("matchOutput(%q,%q) error = %v, wantErr %v", tt.expected, tt.actual, err, tt.wantErr)
			}
		})
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
