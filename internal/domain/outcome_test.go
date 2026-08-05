package domain

import (
	"strings"
	"testing"
)

// TestRecordTestOutcomeKeepsLegacyMapInSync: /metrics consumers still read
// TestCases, so recording an outcome must maintain both views.
func TestRecordTestOutcomeKeepsLegacyMapInSync(t *testing.T) {
	m := &Metrics{}

	m.RecordTestOutcome(TestOutcome{Name: "t1", Passed: true, OutputMode: "tolerant", Route: "goTester"})
	m.RecordTestOutcome(TestOutcome{Name: "t2", Kind: TestFailureMismatch, OutputMode: "shape", Route: "goTester"})

	if len(m.TestOutcomes) != 2 {
		t.Fatalf("TestOutcomes = %d entries, want 2", len(m.TestOutcomes))
	}
	if !m.TestCases["t1"] || m.TestCases["t2"] {
		t.Errorf("legacy TestCases map out of sync: %v", m.TestCases)
	}
	if m.TestOutcomes[0].Kind != "" {
		t.Error("a passing outcome must carry no failure kind")
	}
}

// TestRecordTestOutcomeDistinguishesFailureKinds is the point of [H1a]: the
// dataset's reading guide treats these four outcomes differently, so a bare
// pass/fail would make a results table unreadable.
func TestRecordTestOutcomeDistinguishesFailureKinds(t *testing.T) {
	m := &Metrics{}
	for _, kind := range []string{
		TestFailureMismatch,
		TestFailureError,
		TestFailureSideEffect,
		TestFailureSetup,
		TestFailureTimeout,
		TestFailureFixture,
	} {
		m.RecordTestOutcome(TestOutcome{Name: kind, Kind: kind})
	}

	seen := map[string]bool{}
	for _, o := range m.TestOutcomes {
		if o.Passed {
			t.Errorf("outcome %q should be a failure", o.Name)
		}
		seen[o.Kind] = true
	}
	if len(seen) != 6 {
		t.Errorf("expected six distinct failure kinds, got %v", seen)
	}
}

// TestRecordTestOutcomeTruncatesDetail keeps a run-log line readable; the
// full evidence lives in TestingError.Failures.
func TestRecordTestOutcomeTruncatesDetail(t *testing.T) {
	m := &Metrics{}
	m.RecordTestOutcome(TestOutcome{Name: "t1", Detail: strings.Repeat("x", maxOutcomeDetail*2)})

	got := m.TestOutcomes[0].Detail
	if len(got) <= maxOutcomeDetail {
		t.Errorf("detail should keep the first %d chars, got %d", maxOutcomeDetail, len(got))
	}
	if !strings.HasSuffix(got, "[truncated]") {
		t.Error("truncation should be visible in the value")
	}
}
