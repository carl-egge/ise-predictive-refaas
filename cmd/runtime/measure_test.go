package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// -- the two-point split ---------------------------------------------------

// The whole tool rests on this arithmetic, so it is pinned against a case
// with a known answer rather than only exercised end to end.
func TestTwoPointSplitRecoversKnownCosts(t *testing.T) {
	const (
		startup       = 50 * time.Millisecond
		perInvocation = 2 * time.Millisecond
		n             = 100
	)
	point1 := Sample{Duration: startup + perInvocation}
	pointN := Sample{Duration: startup + n*perInvocation}

	got := twoPointSplit(point1, pointN, n)

	if !approx(got.steadySeconds, perInvocation.Seconds(), 1e-9) {
		t.Errorf("steady = %.6f s, want %.6f", got.steadySeconds, perInvocation.Seconds())
	}
	if !approx(got.startupSeconds, startup.Seconds(), 1e-9) {
		t.Errorf("startup = %.6f s, want %.6f", got.startupSeconds, startup.Seconds())
	}
}

func TestTwoPointSplitSeparatesEnergyTheSameWay(t *testing.T) {
	point1 := Sample{Duration: 51 * time.Millisecond, Joules: 5.2, HasEnergy: true}
	pointN := Sample{Duration: 250 * time.Millisecond, Joules: 25.0, HasEnergy: true}

	got := twoPointSplit(point1, pointN, 100)

	wantSteady := (25.0 - 5.2) / 99
	if !approx(got.steadyJoules, wantSteady, 1e-9) {
		t.Errorf("steady joules = %.6f, want %.6f", got.steadyJoules, wantSteady)
	}
	if !approx(got.startupJoules, 5.2-wantSteady, 1e-9) {
		t.Errorf("startup joules = %.6f, want %.6f", got.startupJoules, 5.2-wantSteady)
	}
	if !got.hasEnergy {
		t.Error("energy should be reported when both points carried it")
	}
}

// A non-positive energy delta means the counters could not resolve the
// per-invocation cost. Reporting zero there would make the function look free
// to run and send its N* to infinity, so no energy must be claimed at all.
func TestTwoPointSplitWithholdsUnresolvableEnergy(t *testing.T) {
	point1 := Sample{Duration: 50 * time.Millisecond, Joules: 5.0, HasEnergy: true}
	pointN := Sample{Duration: 250 * time.Millisecond, Joules: 4.9, HasEnergy: true}

	got := twoPointSplit(point1, pointN, 100)

	if got.hasEnergy {
		t.Error("a non-positive energy delta must not be reported as an energy figure")
	}
	if got.steadyJoules != 0 {
		t.Errorf("steady joules = %v, want 0", got.steadyJoules)
	}
	// Timing must still come through: it resolved even though energy did not.
	if got.steadySeconds <= 0 {
		t.Error("timing should still be reported when only energy is unresolvable")
	}
}

func TestTwoPointSplitNeverReportsNegativeStartup(t *testing.T) {
	// Superlinear growth (a leak, or a cache effect) can push the implied
	// startup below zero; a negative cost is never a valid answer.
	point1 := Sample{Duration: 1 * time.Millisecond}
	pointN := Sample{Duration: 500 * time.Millisecond}

	got := twoPointSplit(point1, pointN, 10)

	if got.startupSeconds < 0 {
		t.Errorf("startup = %v, must be clamped at 0", got.startupSeconds)
	}
}

func TestApplySplitDerivesColdStart(t *testing.T) {
	var m Measurement
	m.applySplit(split{
		steadySeconds: 0.002, startupSeconds: 0.05,
		steadyJoules: 0.2, startupJoules: 5.0, hasEnergy: true,
	})
	if !approx(m.ColdSeconds, 0.052, 1e-9) {
		t.Errorf("cold seconds = %v, want startup+steady = 0.052", m.ColdSeconds)
	}
	if !approx(m.ColdJoules, 5.2, 1e-9) {
		t.Errorf("cold joules = %v, want 5.2", m.ColdJoules)
	}
}

// -- payload stream --------------------------------------------------------

func TestBuildStreamRepeatsPayloadsOneLineEach(t *testing.T) {
	payloads := [][]byte{[]byte(`{"a":1}`), []byte(`{"b":2}`)}
	stream := string(buildStream(payloads, 5))
	want := "{\"a\":1}\n{\"b\":2}\n{\"a\":1}\n{\"b\":2}\n{\"a\":1}\n"
	if stream != want {
		t.Errorf("stream =\n%q\nwant\n%q", stream, want)
	}
}

// Both languages must be handed byte-identical input, or the comparison is
// between two different workloads.
func TestBuildStreamIsDeterministic(t *testing.T) {
	payloads := [][]byte{[]byte(`{"a":1}`), []byte(`{"b":2}`), []byte(`{"c":3}`)}
	first := string(buildStream(payloads, 20))
	for i := 0; i < 5; i++ {
		if got := string(buildStream(payloads, 20)); got != first {
			t.Fatal("buildStream is not deterministic; the two sides could see different input")
		}
	}
}

// Multi-line fixture payloads would break the line-oriented harnesses, so
// they are compacted before use.
func TestCollectPayloadsCompactsOntoOneLine(t *testing.T) {
	raw := json.RawMessage("{\n  \"key\": \"value\",\n  \"nested\": {\n    \"n\": 1\n  }\n}")
	out := compactPayloadForTest(t, raw)
	if len(out) == 0 {
		t.Fatal("payload was dropped")
	}
	for _, b := range out {
		if b == '\n' {
			t.Fatalf("compacted payload still contains a newline: %q", out)
		}
	}
}

func compactPayloadForTest(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	out, err := compactJSON(raw)
	if err != nil {
		t.Fatalf("compactJSON: %v", err)
	}
	return out
}

// -- meters ----------------------------------------------------------------

// The central honesty property: no counters and no stated power means no
// joules, not a plausible-looking zero.
func TestTimeMeterReportsNoEnergyWithoutWatts(t *testing.T) {
	m := &timeMeter{}
	s, err := m.Measure(func() error { time.Sleep(2 * time.Millisecond); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if s.HasEnergy {
		t.Error("time meter must not report energy without -watts")
	}
	if s.Duration <= 0 {
		t.Error("duration should still be measured")
	}
}

func TestTimeMeterMarksDerivedEnergy(t *testing.T) {
	m := &timeMeter{watts: 10}
	s, err := m.Measure(func() error { time.Sleep(10 * time.Millisecond); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !s.HasEnergy {
		t.Fatal("energy should be derived when watts are given")
	}
	if !s.Derived {
		t.Error("a P*t figure must be tagged as derived, or it reads as measured")
	}
	if want := 10 * s.Duration.Seconds(); !approx(s.Joules, want, 1e-9) {
		t.Errorf("joules = %v, want %v", s.Joules, want)
	}
}

func TestTimeMeterDescribesItsBasis(t *testing.T) {
	if got := (&timeMeter{}).Describe(); !contains(got, "no joules") {
		t.Errorf("describe should say no energy is reported, got %q", got)
	}
	if got := (&timeMeter{watts: 12}).Describe(); !contains(got, "DERIVED") {
		t.Errorf("describe should flag derivation, got %q", got)
	}
}

func TestNewMeterRejectsUnknownBackend(t *testing.T) {
	if _, err := NewMeter("guess", 0); err == nil {
		t.Fatal("an unknown meter name must be an error, not a silent fallback")
	}
}

// Auto-detection must always yield a usable meter, even where no counters
// exist - the run still produces timings.
func TestNewMeterAutoAlwaysSucceeds(t *testing.T) {
	m, err := NewMeter("", 0)
	if err != nil {
		t.Fatalf("auto-detection should always find a usable backend: %v", err)
	}
	if m.Describe() == "" {
		t.Error("a meter must describe its provenance")
	}
}

// RAPL counters wrap; a wrap must not become negative energy.
func TestCounterDeltaHandlesWraparound(t *testing.T) {
	cases := []struct {
		name                    string
		before, after, maxRange uint64
		want                    uint64
	}{
		{"normal", 100, 250, 1000, 150},
		{"wrapped", 900, 100, 1000, 200},
		{"wrapped without range", 900, 100, 0, 0},
		{"equal", 500, 500, 1000, 0},
	}
	for _, tc := range cases {
		if got := counterDelta(tc.before, tc.after, tc.maxRange); got != tc.want {
			t.Errorf("%s: counterDelta(%d,%d,%d) = %d, want %d",
				tc.name, tc.before, tc.after, tc.maxRange, got, tc.want)
		}
	}
}

func TestParsePerfEnergySumsCounters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perf.csv")
	content := "# started on Sun Aug 24 12:00:00 2026\n" +
		"\n" +
		"12.50,Joules,power/energy-pkg/,1000000,100.00,,\n" +
		"3.25,Joules,power/energy-ram/,1000000,100.00,,\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := parsePerfEnergy(path)
	if err != nil {
		t.Fatal(err)
	}
	if !approx(got, 15.75, 1e-9) {
		t.Errorf("joules = %v, want 15.75", got)
	}
}

func TestParsePerfEnergyErrorsWhenNoCountersPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perf.csv")
	if err := os.WriteFile(path, []byte("# nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := parsePerfEnergy(path); err == nil {
		t.Fatal("missing counters must be an error, not zero joules")
	}
}

// -- runtime.json contract -------------------------------------------------

// The file must match what cmd/energy's ReadRuntimeMeasurements expects, and
// must omit rather than zero-fill functions it could not measure: cmd/energy
// names a missing function, but would cost a zero as "free" and distort the
// median N*.
func TestWriteRuntimeFileOmitsUnmeasuredFunctions(t *testing.T) {
	report := &Report{
		Functions: []FunctionResult{
			{
				FunctionID: "good",
				Python:     &Measurement{SteadyJoules: 0.5, Resolved: true, HasEnergy: true},
				Go:         &Measurement{SteadyJoules: 0.1, Resolved: true, HasEnergy: true},
			},
			{FunctionID: "skipped", Skipped: "no translation"},
			{
				FunctionID: "unresolved",
				Python:     &Measurement{Resolved: false},
				Go:         &Measurement{Resolved: false},
			},
			{
				FunctionID: "no-energy",
				Python:     &Measurement{Resolved: true, HasEnergy: false},
				Go:         &Measurement{Resolved: true, HasEnergy: false},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := writeRuntimeFile(path, report); err != nil {
		t.Fatalf("writeRuntimeFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]map[string]float64
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("output is not the shape cmd/energy reads: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected only the measured function, got %v", out)
	}
	entry, ok := out["good"]
	if !ok {
		t.Fatal("measured function missing")
	}
	// Field names are the contract with cmd/energy.RuntimeMeasurement.
	if entry["python_joules_per_invocation"] != 0.5 || entry["go_joules_per_invocation"] != 0.1 {
		t.Errorf("wrong keys or values: %v", entry)
	}
}

func TestWriteRuntimeFileFailsWhenNothingWasMeasured(t *testing.T) {
	report := &Report{Functions: []FunctionResult{{FunctionID: "a", Skipped: "no translation"}}}
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := writeRuntimeFile(path, report); err == nil {
		t.Fatal("writing nothing must be an error, not an empty file that looks like a result")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("no file should be written when there is nothing to report")
	}
}

func TestMeasurableRequiresBothSidesResolved(t *testing.T) {
	resolved := &Measurement{Resolved: true, HasEnergy: true}
	unresolved := &Measurement{Resolved: false, HasEnergy: true}

	if !(FunctionResult{Python: resolved, Go: resolved}).Measurable() {
		t.Error("both sides resolved with energy should be measurable")
	}
	if (FunctionResult{Python: resolved, Go: unresolved}).Measurable() {
		t.Error("one unresolved side must make the pair unmeasurable")
	}
	if (FunctionResult{Python: resolved, Go: resolved, Skipped: "x"}).Measurable() {
		t.Error("a skipped function is never measurable")
	}
}

// -- helpers ---------------------------------------------------------------

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
