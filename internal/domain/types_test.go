package domain

import (
	"encoding/json"
	"slices"
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

// TestGetTestFilesMergesEnv verifies that a fixture's own "env" entries are
// appended after the package-level env instead of being clobbered by it
// (exec.Cmd keeps the last duplicate, so fixture entries win).
func TestGetTestFilesMergesEnv(t *testing.T) {
	dp := &DeploymentPackage{
		Env: []string{"REGION=eu-1", "SHARED=pkg"},
		TestFiles: map[string]string{
			"test/t1.json": `{"input":"{}","output":"{}","env":["SHARED=test","EXTRA=1"]}`,
		},
	}

	for tf, err := range dp.GetTestFiles() {
		if err != nil {
			t.Fatalf("GetTestFiles: %v", err)
		}
		want := []string{"REGION=eu-1", "SHARED=pkg", "SHARED=test", "EXTRA=1"}
		if !slices.Equal(tf.Env, want) {
			t.Errorf("Env = %v, want %v", tf.Env, want)
		}
	}
}
