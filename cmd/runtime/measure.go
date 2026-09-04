package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/evaluation/harness"
)

// Measurement is what one side (Python or Go) cost for one function.
//
// The split is obtained by running the *same* executable twice - once with 1
// payload and once with N - and differencing, rather than by timing inside
// the harness. Two reasons that matters: an in-harness clock would compare
// Go's runtime clock against Python's time module and add a per-language bias
// to the very quantity under test, and module import / package-level
// statements (which run before the first invocation in both languages, and in
// real Lambda too) are then charged to startup where they belong.
//
//	T1 = startup + 1 * per_invocation
//	TN = startup + N * per_invocation
//	per_invocation = (TN - T1) / (N - 1)
//	startup        = T1 - per_invocation
type Measurement struct {
	Language string `json:"language"`

	SteadyJoules   float64 `json:"steady_joules_per_invocation"`
	SteadySeconds  float64 `json:"steady_seconds_per_invocation"`
	StartupJoules  float64 `json:"startup_joules"`
	StartupSeconds float64 `json:"startup_seconds"`
	// ColdJoules is one invocation in a fresh process: startup plus one
	// steady invocation. It is what a cold Lambda actually costs, reported
	// alongside steady because §6 asks for them separately.
	ColdJoules  float64 `json:"cold_joules_per_invocation"`
	ColdSeconds float64 `json:"cold_seconds_per_invocation"`

	// CPU time charged to the process by the kernel, split the same way. The
	// ReFaaS microbenchmark reports CPU usage alongside energy and runtime;
	// it is also what distinguishes "does less work" from "waits less", which
	// wall clock alone cannot.
	SteadyCPUSeconds  float64 `json:"steady_cpu_seconds_per_invocation,omitempty"`
	StartupCPUSeconds float64 `json:"startup_cpu_seconds,omitempty"`
	ColdCPUSeconds    float64 `json:"cold_cpu_seconds_per_invocation,omitempty"`
	// MaxRSSBytes is the peak resident set of the long run. A peak, not a
	// per-invocation cost, so it is reported rather than differenced.
	MaxRSSBytes int64 `json:"max_rss_bytes,omitempty"`
	HasCPU      bool  `json:"has_cpu,omitempty"`

	HasEnergy bool `json:"has_energy"`
	Derived   bool `json:"energy_derived,omitempty"`

	Repetitions  int `json:"repetitions"`
	PayloadsUsed int `json:"payloads_used"`
	Invocations  int `json:"invocations_in_long_run"`

	// Resolved reports whether the two-point difference cleared the
	// measurement noise. When false the steady-state fields are zero and
	// meaningless, and the function is excluded from runtime.json rather
	// than written as costing nothing.
	Resolved bool   `json:"resolved"`
	Note     string `json:"note,omitempty"`

	Error string `json:"error,omitempty"`
}

// runner executes one side's process with a given payload stream.
type runner struct {
	name    string
	command func(input []byte) *exec.Cmd
}

// measurePair performs the two-point measurement for both language sides of
// one function, escalating N for them **together**.
//
// Each point is repeated `reps` times and the **minimum** is taken, not the
// mean: on a shared machine, noise from other processes only ever adds time
// and energy, so the minimum is the closest available estimate of the true
// cost and is far more stable across runs than an average that a single
// scheduling hiccup can dominate. This is the standard practice for
// microbenchmarks and is stated here because it is a methodological choice
// the write-up has to defend.
//
// Why the sides escalate together rather than independently: they are only
// ever compared as a ratio, and a ratio between two measurements taken at
// different N is only sound if per-invocation cost is exactly linear in N.
// It is not guaranteed to be - GC onset and cache behaviour both depend on
// how long the process runs. Measuring both sides at the same N removes the
// assumption instead of relying on it. This was not academic: in
// runtime-report-20260831-190900.json, 25 functions had Python resolved at
// N=2000 against Go resolved at N=200.
//
// The cost is that the faster-resolving side is carried up to the slower
// one's N. That is the intended trade - it buys a comparison that needs no
// linearity argument.
func measurePair(m Meter, runners []runner, payloads [][]byte, invocations, reps, maxInvocations int) ([]Measurement, error) {
	if len(payloads) == 0 {
		return nil, fmt.Errorf("no payloads to run")
	}
	if invocations < 2 {
		return nil, fmt.Errorf("need at least 2 invocations to separate startup from steady state")
	}

	single := buildStream(payloads, 1)
	outs := make([]Measurement, len(runners))
	point1s := make([]Sample, len(runners))

	for i, r := range runners {
		outs[i] = Measurement{
			Language:     r.name,
			Repetitions:  reps,
			PayloadsUsed: len(payloads),
		}
		// One untimed run first: it pays the page-cache and (for Go) any
		// first-exec cost, so the measured points are not charged for warming
		// the machine rather than the runtime.
		if _, err := runOnce(m, r, single, runBudget(1)); err != nil {
			return nil, fmt.Errorf("%s: warm-up failed: %w", r.name, err)
		}
		p1, err := bestOf(m, r, single, reps, runBudget(1))
		if err != nil {
			return nil, fmt.Errorf("%s: single-invocation run: %w", r.name, err)
		}
		point1s[i] = p1
	}

	// Escalate N until the T(N)-T(1) difference clears the measurement noise
	// on *every* side, or the cap is reached.
	//
	// Without this the tool silently reports 0 for any function whose
	// per-invocation work is smaller than the run-to-run scatter of process
	// startup - which on this corpus is most of them, since a handler that
	// parses a small JSON event does microseconds of work against a
	// millisecond of startup. A zero would then propagate into runtime.json
	// as "this function costs nothing to run", making its N* infinite or
	// undefined. Finding an N where the signal exceeds the noise is the
	// measurement working; failing to find one is a result to report, not a
	// zero to invent.
	for {
		pointNs := make([]Sample, len(runners))
		allResolved := true
		for i, r := range runners {
			pointN, err := bestOf(m, r, buildStream(payloads, invocations), reps, runBudget(invocations))
			if err != nil {
				return nil, fmt.Errorf("%s: %d-invocation run: %w", r.name, invocations, err)
			}
			pointNs[i] = pointN
			if !resolves(point1s[i], pointN) {
				allResolved = false
			}
		}

		if !allResolved && invocations < maxInvocations {
			invocations *= 10
			if invocations > maxInvocations {
				invocations = maxInvocations
			}
			continue
		}

		for i := range runners {
			outs[i].Invocations = invocations
			if resolves(point1s[i], pointNs[i]) {
				sp := twoPointSplit(point1s[i], pointNs[i], invocations)
				sp.applyCPUSplit(point1s[i], pointNs[i], invocations)
				outs[i].Resolved = true
				outs[i].applySplit(sp)
				continue
			}
			// Report what *was* resolved - startup dominates entirely - and
			// leave the per-invocation figure unresolved so it is excluded
			// from runtime.json rather than written as zero.
			outs[i].StartupSeconds = point1s[i].Duration.Seconds()
			outs[i].ColdSeconds = point1s[i].Duration.Seconds()
			outs[i].Note = fmt.Sprintf(
				"per-invocation cost is below the noise floor even at %d invocations "+
					"(startup %.3f ms dominates); no steady-state figure reported",
				invocations, point1s[i].Duration.Seconds()*1000)
		}
		return outs, nil
	}
}

// resolves reports whether the two-point difference cleared the measurement
// noise actually observed at both points, plus a floor for the reps==1 case
// where no scatter is available.
func resolves(point1, pointN Sample) bool {
	noise := point1.spreadSeconds() + pointN.spreadSeconds()
	if noise < minResolvableSeconds {
		noise = minResolvableSeconds
	}
	return pointN.Duration.Seconds()-point1.Duration.Seconds() > noise
}

// minResolvableSeconds is the floor on what counts as a real difference,
// used when repetition scatter is unavailable or implausibly small. One
// millisecond is roughly a process-startup's worth of jitter on a loaded
// desktop.
const minResolvableSeconds = 0.001

func bestOf(m Meter, r runner, input []byte, reps int, budget time.Duration) (Sample, error) {
	var best, worst Sample
	for i := 0; i < reps; i++ {
		s, err := runOnce(m, r, input, budget)
		if err != nil {
			return Sample{}, err
		}
		if i == 0 || s.Duration < best.Duration {
			best = s
		}
		if i == 0 || s.Duration > worst.Duration {
			worst = s
		}
	}
	best.worstDuration = worst.Duration
	return best, nil
}

func runOnce(m Meter, r runner, input []byte, budget time.Duration) (Sample, error) {
	cmd := r.command(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &nullWriter{}

	// perf can only account for a process it spawns itself, so it needs the
	// command handed over rather than a closure. The budget goes with it, so
	// a hang under perf is bounded exactly as it is under the other meters.
	if pm, ok := m.(*perfMeter); ok {
		pm.PrepareCommand(cmd)
		pm.SetBudget(budget)
		sample, err := pm.Measure(nil)
		if err != nil {
			return sample, err
		}
		return sample, nil
	}

	sample, runErr := m.Measure(func() error { return runWithTimeout(cmd, budget) })
	if runErr != nil {
		return sample, fmt.Errorf("%w: %s", runErr, truncate(stderr.String(), 400))
	}
	// Only on this path is cmd the measured process itself; under perf it is
	// the wrapper, whose rusage would describe perf rather than the function.
	if cpu, rss, ok := processCPU(cmd.ProcessState); ok {
		sample.CPUSeconds, sample.MaxRSSBytes, sample.HasCPU = cpu, rss, ok
	}
	return sample, nil
}

// buildStream repeats the payloads until `invocations` lines are produced, so
// both languages see byte-identical input.
func buildStream(payloads [][]byte, invocations int) []byte {
	var buf bytes.Buffer
	for i := 0; i < invocations; i++ {
		buf.Write(payloads[i%len(payloads)])
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// nullWriter discards the harness envelopes. They are validated once during
// the correctness check, not on every timed run, where writing them out would
// charge the measurement for I/O that the deployed function never pays.
type nullWriter struct{}

func (*nullWriter) Write(p []byte) (int, error) { return len(p), nil }

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// -- process construction --------------------------------------------------

// pythonRunner runs the original function through evaluation/harness/handler.py.
func pythonRunner(python, harnessPath, sourcePath string, env []string) runner {
	return runner{
		name: "python",
		command: func(input []byte) *exec.Cmd {
			cmd := exec.Command(python, harnessPath, sourcePath)
			cmd.Stdin = bytes.NewReader(input)
			cmd.Env = env
			cmd.Dir = filepath.Dir(sourcePath)
			return cmd
		},
	}
}

// goRunner runs the compiled translation.
func goRunner(binary string, env []string) runner {
	return runner{
		name: "go",
		command: func(input []byte) *exec.Cmd {
			cmd := exec.Command(binary)
			cmd.Stdin = bytes.NewReader(input)
			cmd.Env = env
			cmd.Dir = filepath.Dir(binary)
			return cmd
		},
	}
}

// checkRuns executes one payload and reports whether the side produced a
// usable envelope, so a function that errors on every invocation is reported
// as unmeasurable instead of contributing a meaninglessly fast "runtime".
//
// This matters more than it looks: an exception on entry is the fastest
// possible path through a function, so an unnoticed failure does not just
// lose a data point, it biases the comparison toward whichever side failed.
func checkRuns(r runner, payload []byte) error {
	cmd := r.command(append(append([]byte{}, payload...), '\n'))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", r.name, err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s: %w: %s", r.name, err, truncate(stderr.String(), 400))
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return fmt.Errorf("%s: timed out after 30s", r.name)
	}

	out := stdout.String()
	if !strings.Contains(out, harness.OutputMarker) {
		return fmt.Errorf("%s: no harness envelope in output: %s", r.name, truncate(stderr.String(), 300))
	}

	// Check the envelope's own "error" key, not the text. A function may
	// legitimately *return* an object describing an error - several dataset
	// functions answer a bad request with {"status":"error",...} and that is
	// their recorded correct behaviour - and treating that as a harness
	// failure would drop perfectly measurable functions.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(envelopeTail(out)), &envelope); err != nil {
		return fmt.Errorf("%s: unparseable envelope: %v", r.name, err)
	}
	if raw, failed := envelope["error"]; failed {
		return fmt.Errorf("%s: invocation raised: %s", r.name, truncate(string(raw), 300))
	}
	return nil
}

func envelopeTail(out string) string {
	if i := strings.LastIndex(out, harness.OutputMarker); i >= 0 {
		return out[i+len(harness.OutputMarker):]
	}
	return out
}

// hostEnvWithout returns the process environment with the given prefixes
// removed, used to keep ambient AWS credentials away from both sides.
func hostEnvWithout(prefixes ...string) []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		key, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, p := range prefixes {
			if strings.HasPrefix(key, p) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// split is the result of the two-point difference: what one invocation costs
// once the process is up, and what getting it up cost.
type split struct {
	steadySeconds  float64
	startupSeconds float64
	steadyJoules   float64
	startupJoules  float64
	hasEnergy      bool
	derived        bool

	steadyCPUSeconds  float64
	startupCPUSeconds float64
	maxRSSBytes       int64
	hasCPU            bool
}

// twoPointSplit derives per-invocation and startup costs from the two
// measurement points:
//
//	T1 = startup + 1 * per_invocation
//	TN = startup + N * per_invocation
//
// Kept as pure arithmetic, separate from the process spawning, so the
// methodology this whole tool rests on is testable without a machine that has
// energy counters.
//
// Energy is reported only when both points carried it *and* the difference
// came out positive. A non-positive energy delta means the counters could not
// resolve the per-invocation cost even though the clock could; claiming zero
// joules per invocation there would make the function look free to run and
// send its N* to infinity.
func twoPointSplit(point1, pointN Sample, invocations int) split {
	n := float64(invocations)
	s := split{}

	s.steadySeconds = (pointN.Duration.Seconds() - point1.Duration.Seconds()) / (n - 1)
	s.startupSeconds = point1.Duration.Seconds() - s.steadySeconds
	if s.startupSeconds < 0 {
		s.startupSeconds = 0
	}

	if !point1.HasEnergy || !pointN.HasEnergy {
		return s
	}
	steadyJoules := (pointN.Joules - point1.Joules) / (n - 1)
	if steadyJoules <= 0 {
		return s
	}
	startupJoules := point1.Joules - steadyJoules
	if startupJoules < 0 {
		startupJoules = 0
	}
	s.steadyJoules = steadyJoules
	s.startupJoules = startupJoules
	s.hasEnergy = true
	s.derived = point1.Derived || pointN.Derived
	return s
}

// applyCPUSplit adds the CPU columns to a split.
//
// Same two-point difference as time and energy, and non-positive deltas are
// dropped for the same reason: a function whose per-invocation CPU is below
// the accounting granularity (the kernel charges CPU in clock ticks, so a
// short run can land on the same tick count twice) must report "not resolved"
// rather than "runs for free".
//
// MaxRSS is carried over from the long run rather than differenced - it is a
// peak, and the difference of two peaks means nothing.
func (s *split) applyCPUSplit(point1, pointN Sample, invocations int) {
	if !point1.HasCPU || !pointN.HasCPU {
		return
	}
	n := float64(invocations)
	steady := (pointN.CPUSeconds - point1.CPUSeconds) / (n - 1)
	if steady <= 0 {
		return
	}
	startup := point1.CPUSeconds - steady
	if startup < 0 {
		startup = 0
	}
	s.steadyCPUSeconds = steady
	s.startupCPUSeconds = startup
	s.maxRSSBytes = pointN.MaxRSSBytes
	s.hasCPU = true
}

// applySplit copies a split onto the measurement, deriving the cold-start
// figures (one invocation in a fresh process) from it.
func (m *Measurement) applySplit(s split) {
	m.SteadySeconds = s.steadySeconds
	m.StartupSeconds = s.startupSeconds
	m.ColdSeconds = s.startupSeconds + s.steadySeconds
	if s.hasEnergy {
		m.SteadyJoules = s.steadyJoules
		m.StartupJoules = s.startupJoules
		m.ColdJoules = s.startupJoules + s.steadyJoules
		m.HasEnergy = true
		m.Derived = s.derived
	}
	if s.hasCPU {
		m.SteadyCPUSeconds = s.steadyCPUSeconds
		m.StartupCPUSeconds = s.startupCPUSeconds
		m.ColdCPUSeconds = s.startupCPUSeconds + s.steadyCPUSeconds
		m.MaxRSSBytes = s.maxRSSBytes
		m.HasCPU = true
	}
}
