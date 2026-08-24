package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// perfMeter wraps `perf stat -e power/energy-pkg/,power/energy-ram/`, the
// method EVALUATION.md §6 names. It is the second choice rather than the
// first because it needs perf installed and a permissive
// kernel.perf_event_paranoid, while the RAPL sysfs counters it reads
// underneath are often available directly.
//
// perf measures a *child process*, so this backend can only measure work the
// driver launches as one. That is exactly what the driver does, and the
// contract is enforced by Measure requiring the callback to have registered a
// command through PrepareCommand.
type perfMeter struct {
	perfPath string
	events   []string
	// pending is the command the next Measure call should wrap. Set by
	// PrepareCommand immediately before Measure, so the two cannot drift.
	pending *exec.Cmd
}

func newPerfMeter() (*perfMeter, error) {
	path, err := exec.LookPath("perf")
	if err != nil {
		return nil, fmt.Errorf("perf not on PATH")
	}
	m := &perfMeter{perfPath: path, events: []string{"power/energy-pkg/", "power/energy-ram/"}}

	// Verify the energy events actually resolve before committing to this
	// backend: perf exists on many hosts where the RAPL PMU does not, and
	// discovering that per function would silently zero every measurement.
	if err := m.probe(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *perfMeter) probe() error {
	usable := make([]string, 0, len(m.events))
	for _, event := range m.events {
		cmd := exec.Command(m.perfPath, "stat", "-e", event, "-x", ",", "true")
		out, err := cmd.CombinedOutput()
		if err == nil && !strings.Contains(string(out), "<not supported>") &&
			!strings.Contains(string(out), "not supported") {
			usable = append(usable, event)
		}
	}
	if len(usable) == 0 {
		return fmt.Errorf("perf is present but the RAPL energy events are unavailable " +
			"(kernel.perf_event_paranoid, a VM, or WSL); use -meter rapl or -meter time")
	}
	m.events = usable
	return nil
}

func (m *perfMeter) Kind() MeterKind { return MeterPerf }

func (m *perfMeter) Describe() string {
	return fmt.Sprintf("perf stat -e %s", strings.Join(m.events, ","))
}

// PrepareCommand registers the process the next Measure call must wrap.
func (m *perfMeter) PrepareCommand(cmd *exec.Cmd) { m.pending = cmd }

// Measure wraps the pending command in perf stat. fn is ignored except as the
// signal to run: the command itself was handed over by PrepareCommand, since
// perf can only account for a process it spawns.
func (m *perfMeter) Measure(fn func() error) (Sample, error) {
	cmd := m.pending
	m.pending = nil
	if cmd == nil {
		return Sample{}, fmt.Errorf("perf meter: no command registered; call PrepareCommand first")
	}

	statFile, err := os.CreateTemp("", "perf-stat-*.csv")
	if err != nil {
		return Sample{}, fmt.Errorf("perf meter: %w", err)
	}
	statPath := statFile.Name()
	statFile.Close()
	defer os.Remove(statPath)

	args := []string{"stat", "-x", ",", "-o", statPath}
	for _, e := range m.events {
		args = append(args, "-e", e)
	}
	args = append(args, "--", cmd.Path)
	args = append(args, cmd.Args[1:]...)

	wrapped := exec.Command(m.perfPath, args...)
	wrapped.Env = cmd.Env
	wrapped.Dir = cmd.Dir
	wrapped.Stdin = cmd.Stdin
	wrapped.Stdout = cmd.Stdout
	wrapped.Stderr = cmd.Stderr

	start := time.Now()
	runErr := wrapped.Run()
	elapsed := time.Since(start)

	joules, err := parsePerfEnergy(statPath)
	if err != nil {
		return Sample{Duration: elapsed}, fmt.Errorf("perf meter: %w", err)
	}
	return Sample{Duration: elapsed, Joules: joules, HasEnergy: true}, runErr
}

// parsePerfEnergy sums the energy counters out of perf's CSV output. The
// format is `value,unit,event,...` with `#` comment lines; energy events
// report Joules directly.
func parsePerfEnergy(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var total float64
	var found bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 || !strings.Contains(fields[2], "energy") {
			continue
		}
		// perf localises the decimal separator in some builds.
		value, err := strconv.ParseFloat(strings.ReplaceAll(fields[0], ",", "."), 64)
		if err != nil {
			continue
		}
		total += value
		found = true
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("no energy counters in perf output (events unsupported on this host)")
	}
	return total, nil
}
