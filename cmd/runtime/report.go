package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// logProgress prints one line per function as it completes, so a run over 95
// functions is followable rather than silent for an hour.
func logProgress(f FunctionResult) {
	if f.Skipped != "" {
		fmt.Fprintf(os.Stderr, "  %-10s SKIP  %s\n", f.FunctionID, f.Skipped)
		return
	}
	if !f.Python.Resolved || !f.Go.Resolved {
		note := f.Python.Note
		if note == "" {
			note = f.Go.Note
		}
		fmt.Fprintf(os.Stderr, "  %-10s UNRESOLVED  %s\n", f.FunctionID, note)
		return
	}
	speedup := ratio(f.Python.SteadySeconds, f.Go.SteadySeconds)
	cpu := ""
	if f.Python.HasCPU && f.Go.HasCPU {
		cpu = fmt.Sprintf("  cpu %.3f/%.3f ms", f.Python.SteadyCPUSeconds*1000, f.Go.SteadyCPUSeconds*1000)
	}
	if f.Python.HasEnergy && f.Go.HasEnergy {
		fmt.Fprintf(os.Stderr, "  %-10s py %8.3f ms / %7.3f J    go %8.3f ms / %7.3f J    x%.1f%s\n",
			f.FunctionID,
			f.Python.SteadySeconds*1000, f.Python.SteadyJoules,
			f.Go.SteadySeconds*1000, f.Go.SteadyJoules, speedup, cpu)
		return
	}
	fmt.Fprintf(os.Stderr, "  %-10s py %8.3f ms    go %8.3f ms    x%.1f%s  (no energy)\n",
		f.FunctionID, f.Python.SteadySeconds*1000, f.Go.SteadySeconds*1000, speedup, cpu)
}

// printSummary reports what was measured, what was not, and - most
// importantly - on what basis, so a number copied out of here into the thesis
// carries its provenance with it.
func printSummary(w io.Writer, r *Report, outPath string) {
	fmt.Fprintf(w, "\nRuntime measurement (%d functions attempted)\n", len(r.Functions))
	fmt.Fprintf(w, "  meter:       %s - %s\n", r.Meter, r.MeterDetail)
	fmt.Fprintf(w, "  method:      two-point split, %d invocations (both sides at the same N), best of %d\n",
		r.Invocations, r.Repetitions)

	var measured, skipped, unresolved int
	var speedups, coldSpeedups, cpuRatios []float64
	byBucket := map[string][]float64{}
	byAWS := map[bool][]float64{}

	for _, f := range r.Functions {
		if f.Skipped != "" {
			skipped++
			continue
		}
		// An unresolved per-invocation figure is zero, not small; averaging
		// it in would drag every median toward a number nothing measured.
		if !f.Python.Resolved || !f.Go.Resolved {
			unresolved++
			continue
		}
		measured++
		s := ratio(f.Python.SteadySeconds, f.Go.SteadySeconds)
		speedups = append(speedups, s)
		coldSpeedups = append(coldSpeedups, ratio(f.Python.ColdSeconds, f.Go.ColdSeconds))
		// CPU is reported over the functions that resolved it, which need not
		// be all of them: the kernel charges CPU in clock ticks, so a
		// function can resolve on the clock and not on rusage.
		if f.Python.HasCPU && f.Go.HasCPU {
			cpuRatios = append(cpuRatios, ratio(f.Python.SteadyCPUSeconds, f.Go.SteadyCPUSeconds))
		}
		bucket := f.Bucket
		if bucket == "" {
			bucket = "(none)"
		}
		byBucket[bucket] = append(byBucket[bucket], s)
		byAWS[f.AWS] = append(byAWS[f.AWS], s)
	}

	fmt.Fprintf(w, "  measured:    %d\n", measured)
	provisioned := 0
	for _, f := range r.Functions {
		if f.Provisioned && f.Skipped == "" {
			provisioned++
		}
	}
	if provisioned > 0 {
		fmt.Fprintf(w, "  provisioned: %d  (emulator state set up from fixture setup actions)\n", provisioned)
	}
	if skipped > 0 {
		fmt.Fprintf(w, "  skipped:     %d\n", skipped)
	}
	if unresolved > 0 {
		fmt.Fprintf(w, "  unresolved:  %d  (per-invocation work below the noise floor even at -max-invocations)\n", unresolved)
	}
	if measured == 0 {
		fmt.Fprintln(w, "\n  Nothing was measured. The most common cause is -packages not holding a")
		fmt.Fprintln(w, "  translated Go package for these function ids; run the translation first.")
		return
	}

	fmt.Fprintf(w, "\n  Go speedup (python/go, steady state)\n")
	fmt.Fprintf(w, "    median %.1fx   min %.1fx   max %.1fx\n",
		medianOf(speedups), minOf(speedups), maxOf(speedups))
	fmt.Fprintf(w, "  Go speedup (cold start, one process per invocation)\n")
	fmt.Fprintf(w, "    median %.1fx   min %.1fx   max %.1fx\n",
		medianOf(coldSpeedups), minOf(coldSpeedups), maxOf(coldSpeedups))
	if len(cpuRatios) > 0 {
		fmt.Fprintf(w, "  Go CPU-time reduction (python/go, steady state, n=%d)\n", len(cpuRatios))
		fmt.Fprintf(w, "    median %.1fx   min %.1fx   max %.1fx\n",
			medianOf(cpuRatios), minOf(cpuRatios), maxOf(cpuRatios))
	}

	// The dataset's two reporting axes (EVALUATION_DATASET.md §8-§9).
	fmt.Fprintln(w, "\n  By complexity bucket")
	buckets := make([]string, 0, len(byBucket))
	for b := range byBucket {
		buckets = append(buckets, b)
	}
	sort.Strings(buckets)
	for _, b := range buckets {
		fmt.Fprintf(w, "    %-8s n=%-3d median %.1fx\n", b, len(byBucket[b]), medianOf(byBucket[b]))
	}
	fmt.Fprintln(w, "  By AWS usage")
	for _, aws := range []bool{true, false} {
		if len(byAWS[aws]) == 0 {
			continue
		}
		label := "AWS"
		if !aws {
			label = "non-AWS"
		}
		fmt.Fprintf(w, "    %-8s n=%-3d median %.1fx\n", label, len(byAWS[aws]), medianOf(byAWS[aws]))
	}

	for _, note := range r.Notes {
		fmt.Fprintf(w, "\n  NOTE: %s\n", wrapIndent(note, 74, "        "))
	}
	if r.EnergyDerived {
		fmt.Fprintln(w, "\n  Energy figures below are DERIVED from a stated power, not measured.")
		fmt.Fprintln(w, "  Report them as such; the timing ratios above are measurements.")
	}
	fmt.Fprintf(w, "\n  wrote %s\n", outPath)
}

// ratio guards the zero denominator a sub-resolution measurement can produce.
func ratio(python, goSide float64) float64 {
	if goSide <= 0 {
		return 0
	}
	return python / goSide
}

func medianOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

func minOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[0]
}

func maxOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)-1]
}

func wrapIndent(s string, width int, indent string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, word := range words {
		if lineLen > 0 && lineLen+1+len(word) > width {
			b.WriteString("\n" + indent)
			lineLen = 0
		} else if i > 0 {
			b.WriteByte(' ')
			lineLen++
		}
		b.WriteString(word)
		lineLen += len(word)
	}
	return b.String()
}
