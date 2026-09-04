package hostenergy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeMeter builds a Meter over temp files, so the wraparound and
// multi-domain logic is testable without RAPL - which does not exist under
// WSL2, where this is developed.
func fakeMeter(t *testing.T, maxRange uint64, initial ...uint64) (*Meter, []string) {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, len(initial))
	m := &Meter{source: SourceRAPL}
	for i, v := range initial {
		p := filepath.Join(dir, "energy"+string(rune('0'+i))+".uj")
		write(t, p, v)
		paths[i] = p
		m.domains = append(m.domains, &domain{name: p, path: p, maxRange: maxRange})
	}
	m.Sample() // prime, as newRAPL does
	return m, paths
}

func write(t *testing.T, path string, v uint64) {
	t.Helper()
	if err := os.WriteFile(path, []byte(itoa(v)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func TestMeterSumsDomainsAndReportsDelta(t *testing.T) {
	m, paths := fakeMeter(t, 1<<40, 1_000_000, 2_000_000)

	start := m.Sample()
	write(t, paths[0], 3_000_000) // +2 J
	write(t, paths[1], 6_500_000) // +4.5 J
	end := m.Sample()

	joules, ok := end.Since(start)
	if !ok {
		t.Fatal("delta not usable")
	}
	if joules < 6.49 || joules > 6.51 {
		t.Errorf("joules = %v, want 6.5 (both package domains summed)", joules)
	}
}

// TestMeterSurvivesCounterWraparound covers the one way a counter difference
// silently lies: the register is a fixed-width microjoule value that rolls
// over. Untreated, a wrap turns a real cost into a negative number, and the
// job that happened to straddle it into the cheapest in the run.
func TestMeterSurvivesCounterWraparound(t *testing.T) {
	const max = 10_000_000 // 10 J range
	m, paths := fakeMeter(t, max, 9_000_000)

	start := m.Sample()
	write(t, paths[0], 500_000) // wrapped: 1 J to the top, then 0.5 J
	end := m.Sample()

	joules, ok := end.Since(start)
	if !ok {
		t.Fatal("delta not usable across a wrap")
	}
	if joules < 1.49 || joules > 1.51 {
		t.Errorf("joules = %v, want 1.5 across the wrap", joules)
	}
}

// TestUnusableReadingsAreReportedNotZeroed is the honesty rule: a zero is
// indistinguishable from "this job cost nothing", which is never true, so an
// unmetered host has to say so.
func TestUnusableReadingsAreReportedNotZeroed(t *testing.T) {
	var nilMeter *Meter
	s := nilMeter.Sample()
	if s.Source != "" {
		t.Errorf("a nil meter reported source %q, want none", s.Source)
	}
	if _, ok := s.Since(nilMeter.Sample()); ok {
		t.Error("an unmetered host must not report a usable delta")
	}

	m, _ := fakeMeter(t, 1<<40, 1_000)
	real1 := m.Sample()
	if _, ok := real1.Since(s); ok {
		t.Error("mixing a metered and an unmetered snapshot must not produce a delta")
	}

	// A counter that moves backwards further than a wrap explains is a
	// broken reading, not a negative cost.
	backwards := Snapshot{Joules: 5, At: time.Now(), Source: SourceRAPL}
	if _, ok := backwards.Since(Snapshot{Joules: 500, Source: SourceRAPL}); ok {
		t.Error("a backwards counter must not produce a usable delta")
	}
}

// TestNewDegradesWhereThereIsNoCounter pins the WSL2/macOS/container path:
// New must fail cleanly so callers record "no host measurement" rather than
// crashing or inventing one.
func TestNewDegradesWhereThereIsNoCounter(t *testing.T) {
	// The directory exists but is empty under WSL2, so the presence of
	// /sys/class/powercap proves nothing - only a matching domain does.
	if entries, _ := filepath.Glob(raplGlob); len(entries) > 0 {
		t.Skip("this host has readable RAPL domains; the degraded path cannot be exercised here")
	}
	m, err := New()
	if err == nil {
		t.Fatalf("expected ErrUnavailable on a host without powercap, got meter %v", m)
	}
	if m != nil {
		t.Error("a failed New must not return a usable meter")
	}
}
