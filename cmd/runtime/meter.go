package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Energy measurement backends, in preference order.
//
// The rule this file exists to enforce: **never report a joule that was not
// measured or explicitly derived from a stated constant.** cmd/energy already
// holds that line for the LLM side ("until [H6] exists the tool says so
// instead of inventing numbers"), and a runtime figure invented here would
// propagate straight into N* and into the thesis's central claim.
//
// So there are exactly three sources, and every result carries which one
// produced it:
//
//   - MeterRAPL   - Intel/AMD RAPL counters read from /sys/class/powercap.
//     Direct hardware energy, no root, no perf. The primary.
//   - MeterPerf   - `perf stat -e power/energy-pkg/,power/energy-ram/`.
//     Equivalent counters via perf, for hosts where sysfs is restricted.
//   - MeterTime   - wall-clock only. Emits **no joules** unless the caller
//     supplies -watts explicitly, in which case E = P*t is computed and
//     tagged as derived so the provenance travels with the number.
//
// Neither RAPL nor perf is available under WSL2, which is where this was
// developed; that is the case MeterTime exists for, and it is why the
// distinction is a first-class part of the output rather than a footnote.
type MeterKind string

const (
	MeterRAPL MeterKind = "rapl"
	MeterPerf MeterKind = "perf"
	MeterTime MeterKind = "time"
)

// Sample is one measurement of a process run.
type Sample struct {
	Duration time.Duration
	// Joules is the energy attributed to the run. Valid only when
	// HasEnergy is true.
	Joules    float64
	HasEnergy bool
	// worstDuration is the slowest of the repetitions at this point; the
	// spread between it and Duration is the measurement noise the two-point
	// difference has to clear.
	worstDuration time.Duration
	// Derived marks a figure computed as watts*duration rather than read
	// from a counter.
	Derived bool
}

// Meter measures the energy and duration of a function call.
type Meter interface {
	Kind() MeterKind
	// Measure runs fn and reports what it cost. Implementations must attribute
	// only fn's own consumption, so any setup the caller needs belongs outside.
	Measure(fn func() error) (Sample, error)
	// Describe reports the backend's provenance for the run report.
	Describe() string
}

// NewMeter selects a backend. An explicit kind is honoured or fails; empty
// auto-detects RAPL, then perf, then falls back to time.
//
// watts, when non-zero, lets the time backend derive joules. It is
// deliberately opt-in: defaulting to some plausible package power would make
// every run emit numbers that look measured.
func NewMeter(kind string, watts float64) (Meter, error) {
	switch MeterKind(strings.TrimSpace(strings.ToLower(kind))) {
	case MeterRAPL:
		m, err := newRAPLMeter()
		if err != nil {
			return nil, fmt.Errorf("-meter rapl requested but unusable: %w", err)
		}
		return m, nil
	case MeterPerf:
		m, err := newPerfMeter()
		if err != nil {
			return nil, fmt.Errorf("-meter perf requested but unusable: %w", err)
		}
		return m, nil
	case MeterTime:
		return &timeMeter{watts: watts}, nil
	case "":
		if m, err := newRAPLMeter(); err == nil {
			return m, nil
		}
		if m, err := newPerfMeter(); err == nil {
			return m, nil
		}
		return &timeMeter{watts: watts}, nil
	default:
		return nil, fmt.Errorf("unknown meter %q (want rapl, perf or time)", kind)
	}
}

// -- RAPL ------------------------------------------------------------------

// raplMeter reads the running-average-power-limit energy counters exposed at
// /sys/class/powercap/intel-rapl:*/energy_uj. Each domain is a monotonically
// increasing microjoule counter that wraps at max_energy_range_uj.
//
// Package domains only: the per-domain children (core/uncore/dram) partly
// overlap their parent, so summing everything would double-count. DRAM is
// picked up where it is exposed as a sibling package-level domain.
//
// The "psys" domain is skipped for the same reason one level up. It is a
// top-level sibling of package-0, but it meters the whole SoC and *contains*
// the package domains, so adding it to them counts CPU energy twice. Hosts
// that expose psys (many recent Intel laptops) would otherwise report roughly
// double the joules of a host that does not, which is not comparable.
type raplMeter struct {
	domains []raplDomain
}

type raplDomain struct {
	name     string
	path     string
	maxRange uint64
}

func newRAPLMeter() (*raplMeter, error) {
	entries, err := filepath.Glob("/sys/class/powercap/intel-rapl:[0-9]*")
	if err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("no RAPL powercap domains under /sys/class/powercap")
	}

	m := &raplMeter{}
	for _, dir := range entries {
		// Only top-level packages: intel-rapl:0, not intel-rapl:0:1.
		if strings.Count(filepath.Base(dir), ":") != 1 {
			continue
		}
		if domainName(dir) == "psys" {
			// Overlaps the package domains; see the type comment.
			continue
		}
		energyPath := filepath.Join(dir, "energy_uj")
		if _, err := readUint(energyPath); err != nil {
			// Unreadable counters are the common case on locked-down hosts;
			// report it as unusable rather than silently measuring a subset.
			return nil, fmt.Errorf("RAPL counter %s is not readable (try sudo chmod +r, or use -meter perf): %w", energyPath, err)
		}
		maxRange, err := readUint(filepath.Join(dir, "max_energy_range_uj"))
		if err != nil {
			maxRange = 0
		}
		m.domains = append(m.domains, raplDomain{name: domainName(dir), path: energyPath, maxRange: maxRange})
	}
	if len(m.domains) == 0 {
		return nil, fmt.Errorf("no readable top-level RAPL package domains")
	}
	sort.Slice(m.domains, func(i, j int) bool { return m.domains[i].path < m.domains[j].path })
	return m, nil
}

// domainName returns the powercap domain's human-readable name ("package-0",
// "psys", "dram"), falling back to the sysfs directory name.
func domainName(dir string) string {
	if n, err := os.ReadFile(filepath.Join(dir, "name")); err == nil {
		return strings.TrimSpace(string(n))
	}
	return filepath.Base(dir)
}

func (m *raplMeter) Kind() MeterKind { return MeterRAPL }

func (m *raplMeter) Describe() string {
	names := make([]string, 0, len(m.domains))
	for _, d := range m.domains {
		names = append(names, d.name)
	}
	return fmt.Sprintf("RAPL sysfs counters (%s)", strings.Join(names, ", "))
}

func (m *raplMeter) Measure(fn func() error) (Sample, error) {
	before, err := m.read()
	if err != nil {
		return Sample{}, err
	}
	start := time.Now()
	runErr := fn()
	elapsed := time.Since(start)
	after, err := m.read()
	if err != nil {
		return Sample{}, err
	}

	var total float64
	for i := range m.domains {
		total += float64(counterDelta(before[i], after[i], m.domains[i].maxRange)) / 1e6
	}
	return Sample{Duration: elapsed, Joules: total, HasEnergy: true}, runErr
}

func (m *raplMeter) read() ([]uint64, error) {
	out := make([]uint64, len(m.domains))
	for i, d := range m.domains {
		v, err := readUint(d.path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", d.path, err)
		}
		out[i] = v
	}
	return out, nil
}

// counterDelta handles the counter wrapping at max_energy_range_uj. A wrap
// during a sub-second measurement is unlikely but silently negative energy
// would be worse than handling it.
func counterDelta(before, after, maxRange uint64) uint64 {
	if after >= before {
		return after - before
	}
	if maxRange > before {
		return (maxRange - before) + after
	}
	return 0
}

func readUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

// -- time-only -------------------------------------------------------------

// timeMeter measures duration and, only when the caller states a package
// power, derives energy from it.
//
// This is the honest fallback for a host without energy counters. The derived
// figure is not a measurement and is tagged as such everywhere it surfaces:
// P*t assumes the package draws a constant power for the whole run, which is
// wrong in detail but is exactly the assumption the LLM-side model in
// evaluation/energy.config.json already makes (node_power_watts * time), so
// using it here keeps both halves of the comparison on one method.
type timeMeter struct{ watts float64 }

func (m *timeMeter) Kind() MeterKind { return MeterTime }

func (m *timeMeter) Describe() string {
	if m.watts > 0 {
		return fmt.Sprintf("wall-clock only; energy DERIVED as %.1f W x duration (not measured)", m.watts)
	}
	return "wall-clock only; no energy counters available and no -watts given, so no joules are reported"
}

func (m *timeMeter) Measure(fn func() error) (Sample, error) {
	start := time.Now()
	err := fn()
	elapsed := time.Since(start)

	s := Sample{Duration: elapsed}
	if m.watts > 0 {
		s.Joules = m.watts * elapsed.Seconds()
		s.HasEnergy = true
		s.Derived = true
	}
	return s, err
}

// spreadSeconds is the observed run-to-run scatter at this measurement point,
// used as the noise floor the signal must exceed.
func (s Sample) spreadSeconds() float64 {
	if s.worstDuration <= s.Duration {
		return 0
	}
	return (s.worstDuration - s.Duration).Seconds()
}
