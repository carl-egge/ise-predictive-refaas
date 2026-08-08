package domain

import (
	"encoding/json"
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

// TestBeginTestRoundDiscardsPreviousRound is [A19]: a validation stage runs
// again after every recovery hop and re-runs every fixture, so the outcomes
// must describe the last round rather than accumulate. Before this, a
// three-fixture function validated over three rounds archived nine outcomes
// and recorded a repaired case as both failed and passed.
func TestBeginTestRoundDiscardsPreviousRound(t *testing.T) {
	m := &Metrics{}

	// round 1: t1 passes, t2 fails
	m.BeginTestRound()
	m.RecordTestOutcome(TestOutcome{Name: "t1", Passed: true, Route: "goTester"})
	m.RecordTestOutcome(TestOutcome{Name: "t2", Kind: TestFailureMismatch, Route: "goTester"})

	// round 2, after a repair: both pass
	m.BeginTestRound()
	m.RecordTestOutcome(TestOutcome{Name: "t1", Passed: true, Route: "goTester"})
	m.RecordTestOutcome(TestOutcome{Name: "t2", Passed: true, Route: "goTester"})

	if len(m.TestOutcomes) != 2 {
		t.Fatalf("TestOutcomes = %d entries, want 2 (one per fixture, last round only): %+v",
			len(m.TestOutcomes), m.TestOutcomes)
	}
	for _, o := range m.TestOutcomes {
		if !o.Passed {
			t.Errorf("outcome %q should reflect the repaired state, got %+v", o.Name, o)
		}
	}
	if !m.TestCases["t1"] || !m.TestCases["t2"] {
		t.Errorf("legacy TestCases map out of sync with the last round: %v", m.TestCases)
	}
}

// TestBeginTestRoundDropsFixturesTheNewRoundNoLongerRuns guards the reason
// TestCases is cleared too: left alone, its last-write-wins entries would
// outlive the fixtures that produced them and the two views of one job would
// disagree - the exact failure mode [A19] is about.
func TestBeginTestRoundDropsFixturesTheNewRoundNoLongerRuns(t *testing.T) {
	m := &Metrics{}
	m.BeginTestRound()
	m.RecordTestOutcome(TestOutcome{Name: "gone", Passed: true})

	m.BeginTestRound()
	m.RecordTestOutcome(TestOutcome{Name: "kept", Passed: true})

	if _, ok := m.TestCases["gone"]; ok {
		t.Errorf("a fixture the new round did not run must not survive in TestCases: %v", m.TestCases)
	}
	if len(m.TestCases) != len(m.TestOutcomes) {
		t.Errorf("TestCases (%v) and TestOutcomes (%+v) describe different rounds", m.TestCases, m.TestOutcomes)
	}
}

// TestBeginTestRoundKeepsSerializedShapeStable: /metrics and the archived run
// log are consumed by scripts that expect an object for test_cases, so an
// empty round must not turn it into JSON null.
func TestBeginTestRoundKeepsSerializedShapeStable(t *testing.T) {
	m := &Metrics{}
	m.BeginTestRound()

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshalling metrics: %v", err)
	}
	if !strings.Contains(string(b), `"test_cases":{}`) {
		t.Errorf("test_cases should serialize as an empty object, got: %s", b)
	}
}
