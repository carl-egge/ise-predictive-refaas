package pyscan

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// This is [I3]'s acceptance test, and it is the evidence for a claim the
// thesis depends on: that the predictor's features can be computed for an
// arbitrary upload rather than only for dataset artifacts that happen to
// ship a meta.json.
//
// The evaluation dataset's meta.json was produced externally with radon. If
// this package reproduces those numbers on the same 95 functions, then
// training on our own extractor's output is equivalent to training on the
// dataset's - which is what makes the mechanism general. Where the two
// disagree, our value is what the model trains on, so the disagreement has
// to be measured and bounded rather than assumed away.
//
// Measured on evaluation_set (95 functions):
//
//	cc   - 92/95 exact, 94/95 within 2, Pearson r = 0.9998
//	lloc - r = 0.9936 with a systematic offset of about -4 lines
//
// lloc does not match exactly and is not expected to: radon's raw counter
// works on a different basis than "one per logical line", and the dataset's
// values were computed on a pre-normalisation form of the source we do not
// have. The correlation is what matters for a model feature; the thresholds
// below are set to catch a regression in the extractor, not to chase the
// last few lines.
const (
	minCCExact       = 90    // of 95
	minCCWithinTwo   = 93    // of 95
	minCCCorrelation = 0.995 // Pearson r
	minLLOCCorrel    = 0.98  // Pearson r
	maxLLOCMedianAbs = 8.0   // |median delta| in logical lines
)

type datasetFunction struct {
	name   string
	source string
	metaCC float64
	metaLL float64
}

// loadDataset reads main.py + meta.json out of each evaluation_set artifact.
func loadDataset(t *testing.T) []datasetFunction {
	t.Helper()
	dir := filepath.Join("..", "..", "evaluation", "evaluation_set")
	entries, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil || len(entries) == 0 {
		t.Skipf("evaluation_set not present at %s; skipping calibration", dir)
	}

	out := make([]datasetFunction, 0, len(entries))
	for _, path := range entries {
		fn, ok := readArtifact(t, path)
		if ok {
			out = append(out, fn)
		}
	}
	return out
}

func readArtifact(t *testing.T, path string) (datasetFunction, bool) {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer r.Close()

	fn := datasetFunction{name: filepath.Base(path)}
	var meta struct {
		CC   *float64 `json:"cc"`
		LLOC *float64 `json:"lloc"`
	}
	var haveSource, haveMeta bool

	for _, f := range r.File {
		switch filepath.Base(f.Name) {
		case "main.py":
			if filepath.Dir(f.Name) != "." && filepath.Dir(f.Name) != "" {
				continue // only the archive root's handler
			}
			fn.source = readAll(t, f)
			haveSource = true
		case "meta.json":
			if err := json.Unmarshal([]byte(readAll(t, f)), &meta); err != nil {
				t.Fatalf("%s: unreadable meta.json: %v", path, err)
			}
			haveMeta = true
		}
	}
	if !haveSource || !haveMeta || meta.CC == nil || meta.LLOC == nil {
		return fn, false
	}
	fn.metaCC, fn.metaLL = *meta.CC, *meta.LLOC
	return fn, true
}

func readAll(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("open %s: %v", f.Name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", f.Name, err)
	}
	return string(b)
}

func TestCalibrationAgainstEvaluationSet(t *testing.T) {
	requireScanner(t)
	if os.Getenv("PYSCAN_SKIP_CALIBRATION") != "" {
		t.Skip("PYSCAN_SKIP_CALIBRATION set")
	}
	functions := loadDataset(t)
	if len(functions) < 90 {
		t.Skipf("only %d usable artifacts found; skipping calibration", len(functions))
	}

	ctx := context.Background()
	var (
		ccExact, ccWithin2       int
		metaCC, ourCC            []float64
		metaLL, ourLL, llocDelta []float64
	)

	for _, fn := range functions {
		r, err := Scan(ctx, fn.source)
		if err != nil {
			t.Fatalf("%s: scan failed - every dataset artifact must be parseable: %v", fn.name, err)
		}
		cc, lloc := r.Metric("cc"), r.Metric("lloc")

		if cc == fn.metaCC {
			ccExact++
		}
		if math.Abs(cc-fn.metaCC) <= 2 {
			ccWithin2++
		}
		metaCC, ourCC = append(metaCC, fn.metaCC), append(ourCC, cc)
		metaLL, ourLL = append(metaLL, fn.metaLL), append(ourLL, lloc)
		llocDelta = append(llocDelta, lloc-fn.metaLL)
	}

	n := len(functions)
	ccR, llR := pearson(metaCC, ourCC), pearson(metaLL, ourLL)
	medianDelta := median(llocDelta)

	t.Logf("calibration over %d functions: cc exact=%d within2=%d r=%.4f | lloc r=%.4f median_delta=%+.1f",
		n, ccExact, ccWithin2, ccR, llR, medianDelta)

	if ccExact < minCCExact {
		t.Errorf("cc reproduces meta.json exactly for %d/%d, want at least %d", ccExact, n, minCCExact)
	}
	if ccWithin2 < minCCWithinTwo {
		t.Errorf("cc within 2 of meta.json for %d/%d, want at least %d", ccWithin2, n, minCCWithinTwo)
	}
	if ccR < minCCCorrelation {
		t.Errorf("cc correlation with meta.json = %.4f, want >= %.3f", ccR, minCCCorrelation)
	}
	if llR < minLLOCCorrel {
		t.Errorf("lloc correlation with meta.json = %.4f, want >= %.3f", llR, minLLOCCorrel)
	}
	if math.Abs(medianDelta) > maxLLOCMedianAbs {
		t.Errorf("lloc median delta = %+.1f, want |delta| <= %.0f", medianDelta, maxLLOCMedianAbs)
	}
}

// TestEveryDatasetArtifactYieldsAFeatureVector is the other half of the
// acceptance criterion: parsing is not enough, the full vector must build
// for every function in the corpus. A single failure here would mean a hole
// in the training table that only surfaces after the labelling run.
func TestEveryDatasetArtifactYieldsAFeatureVector(t *testing.T) {
	requireScanner(t)
	functions := loadDataset(t)
	if len(functions) < 90 {
		t.Skipf("only %d usable artifacts found", len(functions))
	}

	ctx := context.Background()
	for _, fn := range functions {
		r, err := Scan(ctx, fn.source)
		if err != nil {
			t.Fatalf("%s: %v", fn.name, err)
		}
		f, err := BuildFeatures(r, nil)
		if err != nil {
			t.Fatalf("%s: BuildFeatures: %v", fn.name, err)
		}
		for i, v := range f.Values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("%s: feature %q is %v - a non-finite value would poison training",
					fn.name, f.Names[i], v)
			}
		}
	}
}

func pearson(xs, ys []float64) float64 {
	mx, my := mean(xs), mean(ys)
	var num, dx, dy float64
	for i := range xs {
		a, b := xs[i]-mx, ys[i]-my
		num += a * b
		dx += a * a
		dy += b * b
	}
	if dx == 0 || dy == 0 {
		return 0
	}
	return num / math.Sqrt(dx*dy)
}

func mean(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func median(v []float64) float64 {
	c := append([]float64(nil), v...)
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j] < c[j-1]; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
	n := len(c)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}
