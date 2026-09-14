package predictor

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validModel is the smallest well-formed logistic regression: one feature,
// identity scaling.
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

// validForest is a two-tree forest over two features whose scores can be worked
// out by hand:
//
//	tree 0:  cc <= 5.5 ? 0.9 : 0.2
//	tree 1:  lloc <= 100.5 ? (cc <= 2.5 ? 1.0 : 0.6) : 0.0
func validForest() *Model {
	return &Model{
		Kind:                 RandomForest,
		FeatureSchemaVersion: 1,
		Features:             []string{"cc", "lloc"},
		Trees: []Tree{
			{
				Feature:   []int{0, -2, -2},
				Threshold: []float64{5.5, -2, -2},
				Left:      []int{1, -1, -1},
				Right:     []int{2, -1, -1},
				Value:     []float64{0.5, 0.9, 0.2},
			},
			{
				Feature:   []int{1, 0, -2, -2, -2},
				Threshold: []float64{100.5, 2.5, -2, -2, -2},
				Left:      []int{1, 2, -1, -1, -1},
				Right:     []int{4, 3, -1, -1, -1},
				Value:     []float64{0.4, 0.8, 1.0, 0.6, 0.0},
			},
		},
		Threshold: 0.5,
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
		{"unsupported kind", func(m *Model) { m.Kind = "gradient_boosting" }, "unsupported model kind"},
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
		{"trees on a logistic regression", func(m *Model) {
			m.Trees = validForest().Trees
		}, "carries no trees"},
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

func TestLoadRejectsMalformedForests(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Model)
		wantErr string
	}{
		{"no trees", func(m *Model) { m.Trees = nil }, "has no trees"},
		{"empty tree", func(m *Model) { m.Trees[0] = Tree{} }, "tree 0: has no nodes"},
		{"short threshold", func(m *Model) { m.Trees[1].Threshold = []float64{1} }, "threshold has 1 entries"},
		{"short value", func(m *Model) { m.Trees[0].Value = nil }, "value has 0 entries"},
		{"child before parent", func(m *Model) { m.Trees[0].Left[0] = 0 }, "must come after"},
		{"child outside tree", func(m *Model) { m.Trees[0].Right[0] = 9 }, "must come after"},
		{"feature index out of range", func(m *Model) { m.Trees[1].Feature[0] = 2 }, "feature index 2"},
		{"leaf value above one", func(m *Model) { m.Trees[0].Value[1] = 1.5 }, "not a probability"},
		{"leaf value below zero", func(m *Model) { m.Trees[1].Value[4] = -0.1 }, "not a probability"},
		{"half a leaf marker", func(m *Model) { m.Trees[0].Right[1] = 2 }, "leaf marker"},
		{"forest with coefficients", func(m *Model) {
			m.Coefficients = []float64{1, 1}
		}, "carries no standardizer or coefficients"},
		{"forest with intercept", func(m *Model) { m.Intercept = 0.3 }, "carries no standardizer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validForest()
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

func TestLoadAcceptsValidForest(t *testing.T) {
	m, err := Load(strings.NewReader(encode(t, validForest())))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Kind != RandomForest || len(m.Trees) != 2 {
		t.Fatalf("loaded %q with %d trees", m.Kind, len(m.Trees))
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
	for _, m := range []*Model{validModel(), validForest()} {
		_, err := m.Score(2, []string{"cc", "lloc"}, []float64{1, 1})
		if err == nil {
			t.Fatalf("%s: expected an error scoring a vector from a different feature schema", m.Kind)
		}
		if !strings.Contains(err.Error(), "schema version") {
			t.Fatalf("%s: error %q should name the schema mismatch", m.Kind, err)
		}
	}
}

func TestScoreRefusesMissingFeature(t *testing.T) {
	// Zero is a legitimate value for most columns, so an imputed zero would be
	// invisible and wrong. It must be an error instead.
	for _, m := range []*Model{validModel(), validForest()} {
		_, err := m.Score(1, []string{"lloc"}, []float64{7})
		if err == nil {
			t.Fatalf("%s: expected an error when the vector lacks a feature the model needs", m.Kind)
		}
		if !strings.Contains(err.Error(), "cc") {
			t.Fatalf("%s: error %q should name the missing column", m.Kind, err)
		}
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

func TestForestScoresMeanOfLeaves(t *testing.T) {
	m := validForest()
	cases := []struct {
		names     []string
		values    []float64
		want      float64
		translate bool
	}{
		// tree 0 -> 0.9, tree 1 -> lloc left, cc left -> 1.0
		{[]string{"cc", "lloc"}, []float64{2, 50}, 0.95, true},
		// same vector, extra column, different order
		{[]string{"n_loops", "lloc", "cc"}, []float64{7, 50, 2}, 0.95, true},
		// tree 0 -> 0.9, tree 1 -> lloc left, cc right -> 0.6
		{[]string{"cc", "lloc"}, []float64{4, 50}, 0.75, true},
		// tree 0 -> 0.2, tree 1 -> lloc right -> 0.0
		{[]string{"cc", "lloc"}, []float64{7, 200}, 0.1, false},
		// exactly on a threshold goes left, as in scikit-learn
		{[]string{"cc", "lloc"}, []float64{5.5, 100.5}, 0.75, true},
	}
	for _, tc := range cases {
		got, err := m.Score(1, tc.names, tc.values)
		if err != nil {
			t.Fatalf("score %v: %v", tc.values, err)
		}
		if math.Abs(got.Score-tc.want) > 1e-12 || got.Translate != tc.translate {
			t.Fatalf("score %v = %v (translate %v), want %v (translate %v)",
				tc.values, got.Score, got.Translate, tc.want, tc.translate)
		}
	}
}

func TestForestComparesAsFloat32(t *testing.T) {
	// scikit-learn walks trees on float32 inputs. A float64 value just above a
	// threshold that rounds to the same float32 goes left there; a float64
	// comparison would send it right.
	threshold := float64(float32(0.3))
	x := math.Nextafter(threshold, 1)
	if x <= threshold || float64(float32(x)) != threshold {
		t.Fatalf("test construction: x=%v threshold=%v", x, threshold)
	}
	m := &Model{
		Kind:                 RandomForest,
		FeatureSchemaVersion: 1,
		Features:             []string{"cc_per_lloc"},
		Trees: []Tree{{
			Feature:   []int{0, -2, -2},
			Threshold: []float64{threshold, -2, -2},
			Left:      []int{1, -1, -1},
			Right:     []int{2, -1, -1},
			Value:     []float64{0.5, 1, 0},
		}},
		Threshold: 0.5,
	}
	got, err := m.Score(1, []string{"cc_per_lloc"}, []float64{x})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if got.Score != 1 {
		t.Fatalf("value rounding to the threshold as float32 should go left (score 1), got %v", got.Score)
	}
}

func TestForestRefusesUnscorableValues(t *testing.T) {
	m := validForest()
	for _, v := range []float64{math.NaN(), math.Inf(1), 1e39} {
		if _, err := m.Score(1, []string{"cc", "lloc"}, []float64{v, 1}); err == nil {
			t.Fatalf("expected an error scoring cc=%v", v)
		}
	}
}

// parityFile is one golden file produced by evaluation/prediction/export_parity.py.
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
// must reproduce the probabilities the offline model actually produces, for
// every model kind that has a golden file (testdata/parity-<kind>.json).
//
// Every reported prediction number comes from scikit-learn. If this reader
// disagrees with it, the service is deploying a different classifier from the
// one that was evaluated - and the disagreement would be a few percent, far too
// small to notice in a log and more than large enough to move decisions near the
// threshold. The golden files are regenerated alongside the model exports.
func TestParityWithScikitLearn(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "parity-*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Skip("no parity fixtures")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read: %v", err)
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
			t.Logf("%s (%s): %d cases, worst probability difference %.2e",
				pf.Model, model.Kind, len(pf.Cases), worst)
		})
	}
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

// BenchmarkScoreShipped measures what one gate decision costs with each shipped
// model kind - the marginal energy of prediction ([I8]) is this time charged at
// the node power in evaluation/energy.config.json.
func BenchmarkScoreShipped(b *testing.B) {
	files, _ := filepath.Glob(filepath.Join("testdata", "parity-*.json"))
	if len(files) == 0 {
		b.Skip("no parity fixtures")
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			b.Fatal(err)
		}
		var pf parityFile
		if err := json.Unmarshal(raw, &pf); err != nil {
			b.Fatal(err)
		}
		model, err := LoadFile(filepath.Join("testdata", pf.Model))
		if err != nil {
			b.Fatal(err)
		}
		c := pf.Cases[0]
		b.Run(string(model.Kind), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := model.Score(model.FeatureSchemaVersion, pf.FeatureNames, c.Values); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
