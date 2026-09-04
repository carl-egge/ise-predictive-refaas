// Package hostenergy measures the energy the machine running the conversion
// pipeline actually consumes, so a translation can be costed as what it really
// spent rather than as its LLM inference alone ([H5]).
//
// Why this exists at all: `E_translation` counted GWDG's inference node and
// nothing else, which made the build, test and scan stages report literally
// 0.0 J in `cmd/energy`'s per-stage table - stages that run `go mod tidy`,
// `go build`, one process per fixture, and Floci's emulator containers. The
// pipeline's own machine was invisible to a model of the pipeline's energy.
//
// The measurement is a counter difference, not an estimate. RAPL exposes a
// monotonically increasing per-package energy counter under
// /sys/class/powercap; reading it either side of a job gives the joules that
// package drew in between, with no assumed wattage anywhere. Where the counter
// is unavailable - WSL2, macOS, most containers - this package says so rather
// than substituting a plausible number, and the analysis tool falls back to an
// explicitly-tagged duration x watts estimate. That division is the same rule
// cmd/runtime's meters already enforce: never report a joule that was not
// measured or explicitly derived from a stated constant.
package hostenergy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SourceRAPL labels energy read from the powercap counters.
const SourceRAPL = "rapl"

// ErrUnavailable is returned by New when no counter can be read here.
var ErrUnavailable = errors.New("hostenergy: no readable energy counter on this host")

// Meter reads a cumulative host energy counter.
//
// It is safe for concurrent use, which matters because the wraparound
// bookkeeping below is stateful: two callers sampling at once must not each
// see half of a wrap.
type Meter struct {
	mu      sync.Mutex
	domains []*domain
	source  string
}

// domain is one RAPL package counter plus the state needed to turn a
// wrapping microjoule register into a monotonic joule total.
type domain struct {
	name     string
	path     string
	maxRange uint64 // max_energy_range_uj; 0 when unknown
	last     uint64
	// wrapped accumulates whole ranges already passed, so Total keeps
	// increasing across a wrap instead of jumping backwards.
	wrapped uint64
	primed  bool
}

// Snapshot is a cumulative reading. Differences between two snapshots from the
// same Meter are the energy drawn in between.
type Snapshot struct {
	Joules float64
	At     time.Time
	Source string
}

// New returns a Meter for this host, or ErrUnavailable.
func New() (*Meter, error) {
	m, err := newRAPL()
	if err != nil {
		return nil, err
	}
	return m, nil
}

var (
	defaultOnce  sync.Once
	defaultMeter *Meter
)

// Default returns the process-wide meter, or nil where the host has no
// readable counter. A nil *Meter is usable - Sample reports an unmetered
// reading - so callers need no branch of their own; the counter is opened
// once because the wraparound bookkeeping is per-Meter state.
func Default() *Meter {
	defaultOnce.Do(func() {
		if m, err := New(); err == nil {
			defaultMeter = m
		}
	})
	return defaultMeter
}

// Source names the backend, for provenance in the run log.
func (m *Meter) Source() string {
	if m == nil {
		return ""
	}
	return m.source
}

// Sample reads the counter. A nil Meter samples successfully with zero
// joules and an empty source, so callers on an unmetered host need no
// branching - the absence of a source is what marks the reading unusable.
func (m *Meter) Sample() Snapshot {
	if m == nil {
		return Snapshot{At: time.Now()}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	total := 0.0
	read := 0
	for _, d := range m.domains {
		v, err := readUint(d.path)
		if err != nil {
			// A domain that disappears mid-run (hotplug, permissions) must
			// not zero the whole reading; the remaining domains still
			// describe most of the package.
			continue
		}
		if d.primed && v < d.last && d.maxRange > 0 {
			d.wrapped += d.maxRange
		}
		d.last, d.primed = v, true
		total += float64(d.wrapped+v) / 1e6
		read++
	}
	if read == 0 {
		return Snapshot{At: time.Now()}
	}
	return Snapshot{Joules: total, At: time.Now(), Source: m.source}
}

// Since returns the joules drawn since prev, and whether the figure is usable.
//
// It reports false rather than a zero for an unmetered host, a reading taken
// before the meter was primed, or a counter that moved backwards further than
// wraparound bookkeeping can explain. A zero would be indistinguishable from
// "this job cost no energy", which is never true.
func (s Snapshot) Since(prev Snapshot) (float64, bool) {
	if s.Source == "" || prev.Source == "" || s.Source != prev.Source {
		return 0, false
	}
	d := s.Joules - prev.Joules
	if d < 0 {
		return 0, false
	}
	return d, true
}

// IdleWatts samples the counter over d with the host otherwise quiet, giving
// the baseline draw that would have happened anyway. It is what separates the
// energy a translation *caused* from the energy merely drawn while it ran -
// 92% of this pipeline's wall clock is spent waiting on a remote LLM API, so
// the two figures are far apart and the analysis has to be able to state both.
func (m *Meter) IdleWatts(d time.Duration) (float64, bool) {
	if m == nil || d <= 0 {
		return 0, false
	}
	start := m.Sample()
	time.Sleep(d)
	end := m.Sample()
	joules, ok := end.Since(start)
	elapsed := end.At.Sub(start.At).Seconds()
	if !ok || elapsed <= 0 {
		return 0, false
	}
	return joules / elapsed, true
}

// -- RAPL -------------------------------------------------------------------

// raplGlob matches the top-level package domains only: intel-rapl:0, never
// intel-rapl:0:1. The sub-domains (core, uncore, dram) are subsets of their
// package, so summing both would double-count.
const raplGlob = "/sys/class/powercap/intel-rapl:[0-9]*"

// domainName returns the powercap domain's human-readable name ("package-0",
// "psys", "dram"), falling back to the sysfs directory name.
func domainName(dir string) string {
	if n, err := os.ReadFile(filepath.Join(dir, "name")); err == nil {
		return strings.TrimSpace(string(n))
	}
	return filepath.Base(dir)
}

func newRAPL() (*Meter, error) {
	entries, err := filepath.Glob(raplGlob)
	if err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("%w: no RAPL powercap domains under /sys/class/powercap", ErrUnavailable)
	}
	sort.Strings(entries)

	m := &Meter{source: SourceRAPL}
	for _, dir := range entries {
		if strings.Count(filepath.Base(dir), ":") != 1 {
			continue // a sub-domain; its energy is already in the package
		}
		if domainName(dir) == "psys" {
			// psys is a top-level *sibling* of package-0 in sysfs but meters
			// the whole SoC and contains the package domains, so adding it
			// counts CPU energy twice - and hosts that expose it (many recent
			// Intel laptops, including the measurement host) would report
			// roughly double the joules of hosts that do not. cmd/runtime's
			// meter skips it for the same reason; the two must agree, or the
			// per-job host energy and the runtime measurement are on
			// different scales.
			continue
		}
		path := filepath.Join(dir, "energy_uj")
		if _, err := readUint(path); err != nil {
			continue // present but unreadable (permissions)
		}
		d := &domain{name: filepath.Base(dir), path: path}
		if max, err := readUint(filepath.Join(dir, "max_energy_range_uj")); err == nil {
			d.maxRange = max
		}
		m.domains = append(m.domains, d)
	}
	if len(m.domains) == 0 {
		return nil, fmt.Errorf("%w: RAPL domains present but none readable", ErrUnavailable)
	}
	// Prime the wraparound state so the first real sample has a predecessor.
	m.Sample()
	return m, nil
}

func readUint(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}
