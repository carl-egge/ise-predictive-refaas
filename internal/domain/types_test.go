package domain

import (
	"encoding/json"
	"testing"
)

// TestTestFileUndeterministicAliases verifies that both the correctly named
// "undeterministic" key and the legacy "deterministic" key (used by existing
// fixtures, e.g. examples/paper/f10 and f14) set UndeterministicResults.
func TestTestFileUndeterministicAliases(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`{"input":"{}","output":"{}"}`, false},
		{`{"undeterministic": true}`, true},
		{`{"undeterministic": false}`, false},
		{`{"deterministic": true}`, true}, // legacy key, historical meaning
		{`{"deterministic": false}`, false},
	}
	for _, c := range cases {
		var tf TestFile
		if err := json.Unmarshal([]byte(c.raw), &tf); err != nil {
			t.Fatalf("unmarshal %s: %v", c.raw, err)
		}
		if tf.UndeterministicResults != c.want {
			t.Errorf("%s: UndeterministicResults = %v, want %v", c.raw, tf.UndeterministicResults, c.want)
		}
	}
}
