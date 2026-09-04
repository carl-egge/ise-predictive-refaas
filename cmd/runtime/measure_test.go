package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
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

// -- CPU columns -----------------------------------------------------------

func TestApplyCPUSplitSeparatesCPUTheSameWay(t *testing.T) {
	point1 := Sample{CPUSeconds: 0.052, MaxRSSBytes: 1 << 20, HasCPU: true}
	pointN := Sample{CPUSeconds: 0.250, MaxRSSBytes: 4 << 20, HasCPU: true}

	var s split
	s.applyCPUSplit(point1, pointN, 100)

	wantSteady := (0.250 - 0.052) / 99
	if !s.hasCPU {
		t.Fatal("hasCPU = false, want true")
	}
	if !approx(s.steadyCPUSeconds, wantSteady, 1e-9) {
		t.Errorf("steady cpu = %.9f s, want %.9f", s.steadyCPUSeconds, wantSteady)
	}
	if !approx(s.startupCPUSeconds, 0.052-wantSteady, 1e-9) {
		t.Errorf("startup cpu = %.9f s, want %.9f", s.startupCPUSeconds, 0.052-wantSteady)
	}
	// MaxRSS is a peak, so it is carried from the long run, never differenced.
	if s.maxRSSBytes != 4<<20 {
		t.Errorf("max rss = %d, want %d (the long run's peak, not a difference)", s.maxRSSBytes, 4<<20)
	}
}

// A non-positive CPU delta means the kernel's clock-tick accounting could not
// resolve the per-invocation cost. Reporting it as zero would say the
// function runs for free, which is the same failure the energy path guards.
func TestApplyCPUSplitDropsUnresolvableDelta(t *testing.T) {
	point1 := Sample{CPUSeconds: 0.040, HasCPU: true}
	pointN := Sample{CPUSeconds: 0.040, HasCPU: true}

	var s split
	s.applyCPUSplit(point1, pointN, 1000)

	if s.hasCPU || s.steadyCPUSeconds != 0 {
		t.Errorf("hasCPU=%v steady=%v, want no CPU figure for a zero delta", s.hasCPU, s.steadyCPUSeconds)
	}
}

// CPU must not be invented when only one point carried rusage - which is the
// perf backend's case, where the wrapped process is `perf stat`, not the
// function.
func TestApplyCPUSplitRequiresBothPoints(t *testing.T) {
	var s split
	s.applyCPUSplit(Sample{CPUSeconds: 0.04, HasCPU: true}, Sample{CPUSeconds: 0.25}, 100)
	if s.hasCPU {
		t.Error("hasCPU = true with only one CPU-carrying point, want false")
	}
}

// -- noise resolution ------------------------------------------------------

func TestResolvesRequiresSignalAboveObservedSpread(t *testing.T) {
	// Spread of 20 ms at each point: a 30 ms difference is inside the noise.
	point1 := Sample{Duration: 100 * time.Millisecond, worstDuration: 120 * time.Millisecond}
	noisyN := Sample{Duration: 130 * time.Millisecond, worstDuration: 150 * time.Millisecond}
	if resolves(point1, noisyN) {
		t.Error("resolves = true for a difference inside the repetition spread, want false")
	}

	clearN := Sample{Duration: 900 * time.Millisecond, worstDuration: 920 * time.Millisecond}
	if !resolves(point1, clearN) {
		t.Error("resolves = false for a difference well above the spread, want true")
	}
}

// -- joint escalation ------------------------------------------------------

// The invariant the paired measurement exists to guarantee: both language
// sides are measured at the same invocation count, even when one of them
// resolves immediately and the other never does.
//
// Before this, measureSide escalated each side independently, and
// runtime-report-20260831-190900.json ended up with 25 functions whose Python
// side was measured at N=2000 against a Go side measured at N=200 - a ratio
// between two different experiments, sound only if per-invocation cost is
// exactly linear in N.
//
// Driven through a fake sampler rather than real processes: the ladder
// decides which N every figure in runtime.json is derived from, so it is
// worth testing deterministically instead of racing process-startup jitter.
func TestEscalateCarriesTheResolvedSideToTheSlowestSideN(t *testing.T) {
	const startup = 100 * time.Millisecond
	point1s := []Sample{
		{Duration: startup}, // flat side: cost never grows with N
		{Duration: startup}, // linear side: 1 ms per invocation, resolves at once
	}

	var asked []int
	sampleAt := func(n int) ([]Sample, error) {
		asked = append(asked, n)
		return []Sample{
			{Duration: startup},
			{Duration: startup + time.Duration(n)*time.Millisecond},
		}, nil
	}

	finalN, pointNs, err := escalate(point1s, sampleAt, 1000, 100000)
	if err != nil {
		t.Fatalf("escalate: %v", err)
	}

	if finalN != 100000 {
		t.Errorf("settled at N=%d, want 100000: the unresolvable side must drive escalation to the cap", finalN)
	}
	if want := []int{1000, 10000, 100000}; !equalInts(asked, want) {
		t.Errorf("sampled at %v, want %v (x10 ladder)", asked, want)
	}
	// The linear side resolved at the very first rung and was still carried
	// up - that is the whole point.
	if resolves(point1s[0], pointNs[0]) {
		t.Error("flat side resolved, want unresolved")
	}
	if !resolves(point1s[1], pointNs[1]) {
		t.Error("linear side did not resolve at the final N, want resolved")
	}
}

// The ladder must stop as soon as *every* side resolves - escalating further
// would multiply the cost of the measurement pass for no added signal.
func TestEscalateStopsWhenAllSidesResolve(t *testing.T) {
	point1s := []Sample{{Duration: time.Millisecond}, {Duration: time.Millisecond}}

	var asked []int
	sampleAt := func(n int) ([]Sample, error) {
		asked = append(asked, n)
		d := time.Millisecond + time.Duration(n)*time.Millisecond
		return []Sample{{Duration: d}, {Duration: d}}, nil
	}

	finalN, _, err := escalate(point1s, sampleAt, 1000, 100000)
	if err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if finalN != 1000 {
		t.Errorf("settled at N=%d, want 1000", finalN)
	}
	if len(asked) != 1 {
		t.Errorf("sampled %d times (%v), want 1 - both sides resolved on the first rung", len(asked), asked)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// End to end through real processes, asserting only the part that cannot be
// flaky: whatever N the ladder settles on, both sides report the same one.
func TestMeasurePairReportsOneNForBothSides(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	meter, err := NewMeter("time", 0)
	if err != nil {
		t.Fatalf("time meter: %v", err)
	}

	shellRunner := func(name, script string) runner {
		return runner{
			name: name,
			command: func(input []byte) *exec.Cmd {
				cmd := exec.Command(sh, "-c", script)
				cmd.Stdin = bytes.NewReader(input)
				return cmd
			},
		}
	}
	cheap := shellRunner("cheap", "cat > /dev/null")
	expensive := shellRunner("expensive",
		"while read -r l; do i=0; while [ $i -lt 2000 ]; do i=$((i+1)); done; done")

	got, err := measurePair(meter, []runner{cheap, expensive}, [][]byte{[]byte(`{"a":1}`)}, 2, 1, 20)
	if err != nil {
		t.Fatalf("measurePair: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d measurements, want 2", len(got))
	}
	if got[0].Invocations != got[1].Invocations {
		t.Errorf("invocation counts differ: %s=%d, %s=%d - both sides must be measured at the same N",
			got[0].Language, got[0].Invocations, got[1].Language, got[1].Invocations)
	}
	if got[0].Invocations == 0 {
		t.Error("no invocation count recorded")
	}
}
