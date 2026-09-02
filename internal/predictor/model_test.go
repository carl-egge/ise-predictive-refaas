package predictor

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validModel is the smallest well-formed model: one feature, identity scaling.
func validModel() *Model {
	return &Model{
		Kind:                 LogisticRegression,
		FeatureSchemaVersion: 1,
		Features:             []string{"cc"},
		Mean:                 []float64{0},
		Scale:                []float64{1},
		Coefficients:         []float64{1},
		Intercept:            0,
		Threshold:            0.5,
	}
}

func encode(t *testing.T, m *Model) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestLoadRejectsMalformedModels(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Model)
		wantErr string
	}{
		{"unsupported kind", func(m *Model) { m.Kind = "random_forest" }, "unsupported model kind"},
		{"no features", func(m *Model) { m.Features = nil }, "names no features"},
		{"short mean", func(m *Model) { m.Mean = nil }, "mean has 0 entries"},
		{"short scale", func(m *Model) { m.Scale = []float64{1, 2} }, "scale has 2 entries"},
		{"short coefficients", func(m *Model) { m.Coefficients = nil }, "coefficients has 0 entries"},
		{"no schema version", func(m *Model) { m.FeatureSchemaVersion = 0 }, "feature schema version"},
		{"threshold out of range", func(m *Model) { m.Threshold = 1.5 }, "not a probability"},
		{"duplicate feature", func(m *Model) {
			m.Features = []string{"cc", "cc"}
			m.Mean = []float64{0, 0}
			m.Scale = []float64{1, 1}
			m.Coefficients = []float64{1, 1}
		}, "appears twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validModel()
			tc.mutate(m)
			_, err := Load(strings.NewReader(encode(t, m)))
			if err == nil {
				t.Fatalf("expected a load error for %s, got none", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	// An unknown field is usually a renamed one, which would otherwise load as
	// a zero value and score silently wrong.
	_, err := Load(strings.NewReader(
		`{"model":"logistic_regression","feature_schema_version":1,"features":["cc"],` +
			`"mean":[0],"scale":[1],"coefficients":[1],"intercept":0,"threshold":0.5,` +
			`"coefficents":[9]}`))
	if err == nil {
		t.Fatal("expected a load error for an unknown field")
	}
}

func TestScoreRefusesSchemaMismatch(t *testing.T) {
	m := validModel()
	_, err := m.Score(2, []string{"cc"}, []float64{1})
	if err == nil {
		t.Fatal("expected an error scoring a vector from a different feature schema")
	}
	if !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("error %q should name the schema mismatch", err)
	}
}

func TestScoreRefusesMissingFeature(t *testing.T) {
	// Zero is a legitimate value for most columns, so an imputed zero would be
	// invisible and wrong. It must be an error instead.
	m := validModel()
	_, err := m.Score(1, []string{"lloc"}, []float64{7})
	if err == nil {
		t.Fatal("expected an error when the vector lacks a feature the model needs")
	}
	if !strings.Contains(err.Error(), "cc") {
		t.Fatalf("error %q should name the missing column", err)
	}
}

func TestScoreResolvesByNameNotPosition(t *testing.T) {
	// The vector carries more columns than the model uses, in a different
	// order. Positional alignment would read the wrong number.
	m := validModel()
	m.Features = []string{"cc", "lloc"}
	m.Mean = []float64{0, 0}
	m.Scale = []float64{1, 1}
	m.Coefficients = []float64{1, 0}

	a, err := m.Score(1, []string{"cc", "lloc", "n_loops"}, []float64{2, 99, 5})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	b, err := m.Score(1, []string{"n_loops", "lloc", "cc"}, []float64{5, 99, 2})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if a.Score != b.Score {
		t.Fatalf("reordering the vector changed the score: %v vs %v", a.Score, b.Score)
	}
	if math.Abs(a.Score-sigmoid(2)) > 1e-12 {
		t.Fatalf("score %v does not match sigmoid(2)=%v", a.Score, sigmoid(2))
	}
}

func TestScoreZeroScaleContributesNothing(t *testing.T) {
	m := validModel()
	m.Scale = []float64{0}
	m.Mean = []float64{10}
	got, err := m.Score(1, []string{"cc"}, []float64{1e9})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if got.Score != 0.5 {
		t.Fatalf("a zero-scale column should contribute nothing, got %v", got.Score)
	}
}

func TestSigmoidStaysFiniteAtExtremes(t *testing.T) {
	// The naive 1/(1+exp(-z)) overflows for strongly negative z, turning a
	// confident skip into NaN - which compares false against any threshold and
	// silently becomes a "translate".
	for _, z := range []float64{-1e4, -800, -50, 0, 50, 800, 1e4} {
		v := sigmoid(z)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("sigmoid(%v) = %v", z, v)
		}
		if v < 0 || v > 1 {
			t.Fatalf("sigmoid(%v) = %v is not a probability", z, v)
		}
	}
}

func TestThresholdDecidesTranslate(t *testing.T) {
	m := validModel()
	m.Threshold = 0.6
	below, err := m.Score(1, []string{"cc"}, []float64{0})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if below.Translate {
		t.Fatalf("score %v should be below the threshold %v", below.Score, m.Threshold)
	}
	above, err := m.Score(1, []string{"cc"}, []float64{5})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if !above.Translate {
		t.Fatalf("score %v should be above the threshold %v", above.Score, m.Threshold)
	}
}

// parityCase is one row of the golden file produced by evaluate.py.
type parityFile struct {
	Model        string   `json:"model"`
	FeatureNames []string `json:"feature_names"`
	Cases        []struct {
		FunctionID   string    `json:"function_id"`
		Values       []float64 `json:"values"`
		SklearnScore float64   `json:"sklearn_score"`
		Translate    bool      `json:"translate"`
	} `json:"cases"`
}

// TestParityWithScikitLearn is the test that matters for [I10]: the Go reader
// must reproduce the probabilities the offline model actually produces.
//
// Every number in [I7] comes from scikit-learn. If this reader disagrees with
// it, the service is deploying a different classifier from the one that was
// evaluated - and the disagreement would be a few percent, far too small to
// notice in a log and more than large enough to move decisions near the
// threshold. The golden file is regenerated alongside the model export.
func TestParityWithScikitLearn(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "parity.json"))
	if err != nil {
		t.Skipf("no parity fixture: %v", err)
	}
	var pf parityFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		t.Fatalf("parity fixture: %v", err)
	}
	model, err := LoadFile(filepath.Join("testdata", pf.Model))
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	if len(pf.Cases) == 0 {
		t.Fatal("parity fixture has no cases")
	}

	const tol = 1e-9
	worst := 0.0
	for _, c := range pf.Cases {
		got, err := model.Score(model.FeatureSchemaVersion, pf.FeatureNames, c.Values)
		if err != nil {
			t.Fatalf("%s: %v", c.FunctionID, err)
		}
		diff := math.Abs(got.Score - c.SklearnScore)
		if diff > worst {
			worst = diff
		}
		if diff > tol {
			t.Errorf("%s: score %.12f, scikit-learn %.12f (diff %.2e)",
				c.FunctionID, got.Score, c.SklearnScore, diff)
		}
		if got.Translate != c.Translate {
			t.Errorf("%s: decision %v, scikit-learn %v (score %.6f, threshold %.6f)",
				c.FunctionID, got.Translate, c.Translate, got.Score, model.Threshold)
		}
	}
	t.Logf("%d cases, worst probability difference %.2e", len(pf.Cases), worst)
}

func TestModelIDPrefersProvenance(t *testing.T) {
	m := validModel()
	if got := m.ModelID(); got != string(LogisticRegression) {
		t.Fatalf("bare model should identify as its kind, got %q", got)
	}
	m.Provenance = map[string]any{"run_id": "20260831-190900"}
	if got := m.ModelID(); got != "logistic_regression@20260831-190900" {
		t.Fatalf("run_id should qualify the kind, got %q", got)
	}
	m.Provenance["id"] = "m1-lr-20260831-190900"
	if got := m.ModelID(); got != "m1-lr-20260831-190900" {
		t.Fatalf("an explicit id should win, got %q", got)
	}
}
