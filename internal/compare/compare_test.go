package compare

import (
	"encoding/json"
	"testing"
)

func decode(t *testing.T, s string) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decoding %q: %v", s, err)
	}
	return v
}

func TestJSONSubsetModes(t *testing.T) {
	tests := []struct {
		name     string
		want     string
		got      string
		mode     Mode
		match    bool
		wantPath string
	}{
		// subset semantics (all modes)
		{"exact match", `{"a":1}`, `{"a":1}`, Strict, true, ""},
		{"extra fields ignored", `{"a":1}`, `{"a":1,"b":2}`, Strict, true, ""},
		{"missing key", `{"a":1,"b":2}`, `{"a":1}`, Strict, false, "b"},
		{"nested divergence path", `{"user":{"name":"Ada"}}`, `{"user":{"name":"Bob"}}`, Strict, false, "user.name"},

		// strict scalars: types and values
		{"strict value mismatch", `{"a":1}`, `{"a":2}`, Strict, false, "a"},
		{"strict stringified number fails", `{"code":200}`, `{"code":"200"}`, Strict, false, "code"},
		{"strict null matches null", `{"a":null}`, `{"a":null}`, Strict, true, ""},
		{"strict null vs value fails", `{"a":null}`, `{"a":1}`, Strict, false, "a"},

		// tolerant scalars: floci's historical leniency
		{"tolerant stringified number passes", `{"code":200}`, `{"code":"200"}`, Tolerant, true, ""},
		{"tolerant different values fail", `{"code":200}`, `{"code":404}`, Tolerant, false, "code"},

		// shape-only: types matter, values don't
		{"shape ignores values", `{"temp":21.5,"city":"HH"}`, `{"temp":3.2,"city":"Berlin"}`, ShapeOnly, true, ""},
		{"shape type mismatch fails", `{"temp":21.5}`, `{"temp":"21.5"}`, ShapeOnly, false, "temp"},
		{"shape structure still required", `{"data":{"x":1}}`, `{"data":[1]}`, ShapeOnly, false, "data.$"},

		// arrays
		{"array equal length elementwise", `{"xs":[1,2]}`, `{"xs":[1,2]}`, Strict, true, ""},
		{"array length mismatch strict", `{"xs":[1,2]}`, `{"xs":[1]}`, Strict, false, "xs.[]"},
		{"array variable length in shape mode", `{"xs":[{"id":1}]}`, `{"xs":[{"id":7},{"id":9},{"id":4}]}`, ShapeOnly, true, ""},
		{"array element shape mismatch", `{"xs":[{"id":1}]}`, `{"xs":[{"id":"seven"}]}`, ShapeOnly, false, "xs.[0].id"},

		// nested JSON-encoded strings (the harness body field)
		{"json strings compared structurally", `{"body":"{\"a\":1,\"b\":2}"}`, `{"body":"{\"b\": 2, \"a\": 1}"}`, Strict, true, ""},
		{"json string vs object", `{"body":{"a":1}}`, `{"body":"{\"a\":1}"}`, Strict, true, ""},
		{"json string value mismatch", `{"body":"{\"a\":1}"}`, `{"body":"{\"a\":2}"}`, Strict, false, "body.a"},

		// top-level type mismatches
		{"object vs scalar", `{"a":1}`, `"nope"`, Strict, false, "$"},
		{"array vs object", `[1]`, `{"a":1}`, Strict, false, "$"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, path := JSONSubset(decode(t, tt.want), decode(t, tt.got), tt.mode)
			if ok != tt.match {
				t.Fatalf("JSONSubset(%s, %s, %v) = %v, want %v (path %q)", tt.want, tt.got, tt.mode, ok, tt.match, path)
			}
			if !ok && tt.wantPath != "" && path != tt.wantPath {
				t.Errorf("divergence path = %q, want %q", path, tt.wantPath)
			}
		})
	}
}
