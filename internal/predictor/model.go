// Package predictor scores an uploaded function's ex-ante feature vector and
// turns the score into a translate/skip decision ([I10]).
//
// It is deliberately a *reader*, not a trainer. The model is fitted offline in
// evaluation/prediction (scikit-learn) and exported as JSON; everything here is
// arithmetic over exported numbers - a dot product over standardized features
// for a logistic regression, a walk down every tree for a random forest. That
// keeps go.mod free of any ML dependency - the same separation cmd/energy keeps
// by holding its constants in evaluation/energy.config.json rather than in code -
// and it means the shipped artifact is a file an examiner can read.
//
// The package imports nothing from internal/pipeline, so it stays usable by the
// pipeline stage, the HTTP endpoint and any offline tool. It deliberately does
// not import internal/domain either: scoring takes the three things a feature
// vector *is*, so a caller holding a domain.FeatureVector, a pyscan.Features or
// a decoded JSON body can all use it without conversion.
package predictor

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

// Kind is the model family an exported file describes.
type Kind string

const (
	// LogisticRegression is a standardizer and a linear score. Its output is a
	// calibrated probability, and the exported model is a short list of
	// coefficients that can be read by hand.
	LogisticRegression Kind = "logistic_regression"

	// RandomForest is scikit-learn's RandomForestClassifier: the score is the
	// mean, over trees, of the positive-class fraction at the leaf the vector
	// reaches, which is exactly predict_proba. It ranks well but is not
	// calibrated. Retrained on the replicate series ([I12]) it is the better
	// in-corpus gate (AUC 0.764 against 0.642, and the only gate with a useful
	// invocation range in every run), while the logistic regression still
	// transfers better to function_set (0.697 against 0.394).
	RandomForest Kind = "random_forest"
)

// leaf marks a node without children, as scikit-learn's TREE_LEAF does.
const leaf = -1

// Model is an exported classifier.
//
// Features names the columns this model was fitted on, in the order its
// coefficients or its trees' feature indices refer to. It is not required to be
// the whole feature vector - the training pipeline drops zero-variance columns,
// and on the evaluation_set corpus 8 of 56 carry no information at all - so
// scoring resolves each name against the supplied vector rather than assuming
// positional alignment.
type Model struct {
	Kind Kind `json:"model"`

	// FeatureSchemaVersion is pyscan.FeatureSchemaVersion as it stood when
	// the model was fitted. Scoring refuses a mismatch: a vector recorded
	// under a different schema may have the same width and different
	// meanings, which would feed the wrong number into the wrong coefficient
	// and produce a confident, wrong answer rather than an error.
	FeatureSchemaVersion int `json:"feature_schema_version"`

	Features []string `json:"features"`

	// Mean, Scale, Coefficients and Intercept describe a logistic regression
	// and must be absent from a random forest, whose trees split on raw values.
	Mean         []float64 `json:"mean,omitempty"`
	Scale        []float64 `json:"scale,omitempty"`
	Coefficients []float64 `json:"coefficients,omitempty"`
	Intercept    float64   `json:"intercept,omitempty"`

	// Trees describe a random forest and must be absent from a logistic
	// regression.
	Trees []Tree `json:"trees,omitempty"`

	// Threshold is the operating point, fitted offline inside the training
	// folds ([I7]). Carried with the model because a probability without the
	// point it is compared against is not a decision, and because choosing
	// one at deploy time would undo the nested selection that makes the
	// reported numbers honest.
	Threshold float64 `json:"threshold"`

	// Provenance is free-form and never read by this package. It exists so a
	// deployed model can be traced back to the run and corpus that produced
	// it without consulting a separate file.
	Provenance map[string]any `json:"provenance,omitempty"`
}

// Tree is one decision tree as flat node arrays in the layout of scikit-learn's
// tree_, with node 0 the root. Node i is a leaf when Left[i] == -1. Otherwise a
// vector goes to Left[i] when its feature Feature[i], cast to float32, is at
// most Threshold[i], and to Right[i] otherwise. Value[i] is the fraction of the
// (bootstrap- and class-weighted) training samples at node i that belong to the
// positive class; only leaves' values are ever read.
type Tree struct {
	Feature   []int     `json:"feature"`
	Threshold []float64 `json:"threshold"`
	Left      []int     `json:"left"`
	Right     []int     `json:"right"`
	Value     []float64 `json:"value"`
}

// Load reads and validates a model. Validation is strict on purpose: a model
// with mismatched slice lengths would score silently and wrongly, and the only
// moment that defect is cheap to catch is at load.
func Load(r io.Reader) (*Model, error) {
	var m Model
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("predictor: cannot decode model: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// LoadFile reads a model from disk.
func LoadFile(path string) (*Model, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("predictor: cannot open model %s: %w", path, err)
	}
	defer f.Close()
	m, err := Load(f)
	if err != nil {
		return nil, fmt.Errorf("%w (%s)", err, path)
	}
	return m, nil
}

func (m *Model) validate() error {
	if m.Kind != LogisticRegression && m.Kind != RandomForest {
		return fmt.Errorf("predictor: unsupported model kind %q (implemented: %q, %q)",
			m.Kind, LogisticRegression, RandomForest)
	}
	if len(m.Features) == 0 {
		return fmt.Errorf("predictor: model names no features")
	}
	n := len(m.Features)
	switch m.Kind {
	case LogisticRegression:
		for _, f := range []struct {
			name string
			len  int
		}{
			{"mean", len(m.Mean)},
			{"scale", len(m.Scale)},
			{"coefficients", len(m.Coefficients)},
		} {
			if f.len != n {
				return fmt.Errorf("predictor: %s has %d entries but the model names %d features",
					f.name, f.len, n)
			}
		}
		if len(m.Trees) > 0 {
			return fmt.Errorf("predictor: a logistic regression carries no trees")
		}
	case RandomForest:
		if len(m.Trees) == 0 {
			return fmt.Errorf("predictor: random forest has no trees")
		}
		// A forest file that also carries coefficients is almost certainly a
		// mislabelled logistic regression; scoring it as trees would ignore them.
		if len(m.Mean) > 0 || len(m.Scale) > 0 || len(m.Coefficients) > 0 || m.Intercept != 0 {
			return fmt.Errorf("predictor: a random forest carries no standardizer or coefficients")
		}
		for i := range m.Trees {
			if err := m.Trees[i].validate(n); err != nil {
				return fmt.Errorf("predictor: tree %d: %w", i, err)
			}
		}
	}
	if m.FeatureSchemaVersion <= 0 {
		return fmt.Errorf("predictor: model does not record a feature schema version")
	}
	if m.Threshold < 0 || m.Threshold > 1 {
		return fmt.Errorf("predictor: threshold %v is not a probability", m.Threshold)
	}
	seen := make(map[string]bool, n)
	for _, name := range m.Features {
		if seen[name] {
			return fmt.Errorf("predictor: feature %q appears twice", name)
		}
		seen[name] = true
	}
	return nil
}

func (t *Tree) validate(nFeatures int) error {
	nodes := len(t.Left)
	if nodes == 0 {
		return fmt.Errorf("has no nodes")
	}
	for _, f := range []struct {
		name string
		len  int
	}{
		{"feature", len(t.Feature)},
		{"threshold", len(t.Threshold)},
		{"right", len(t.Right)},
		{"value", len(t.Value)},
	} {
		if f.len != nodes {
			return fmt.Errorf("%s has %d entries but left has %d", f.name, f.len, nodes)
		}
	}
	for i := 0; i < nodes; i++ {
		l, r := t.Left[i], t.Right[i]
		if l == leaf {
			if r != leaf {
				return fmt.Errorf("node %d has a leaf marker on the left but right child %d", i, r)
			}
			if v := t.Value[i]; !(v >= 0 && v <= 1) {
				return fmt.Errorf("leaf %d has value %v, which is not a probability", i, v)
			}
			continue
		}
		// scikit-learn numbers children after their parent. Requiring it is what
		// guarantees that a walk from the root reaches a leaf: every step moves
		// to a larger index, and a cycle would otherwise loop forever at scoring.
		if l <= i || r <= i || l >= nodes || r >= nodes {
			return fmt.Errorf("node %d has children %d and %d; children must come after "+
				"their parent and lie inside the tree's %d nodes", i, l, r, nodes)
		}
		if f := t.Feature[i]; f < 0 || f >= nFeatures {
			return fmt.Errorf("node %d splits on feature index %d, but the model names %d features",
				i, f, nFeatures)
		}
		if th := t.Threshold[i]; math.IsNaN(th) || math.IsInf(th, 0) {
			return fmt.Errorf("node %d has threshold %v", i, th)
		}
	}
	return nil
}

// Prediction is one scored function.
type Prediction struct {
	// Score is the model's P(the pipeline translates this function
	// successfully): a calibrated probability for a logistic regression, the
	// mean positive-class leaf fraction over trees for a random forest.
	Score float64 `json:"score"`
	// Translate is Score >= Threshold.
	Translate bool    `json:"translate"`
	Threshold float64 `json:"threshold"`
	// Model identifies what produced the score, so a run log row stays
	// interpretable after the deployed model is replaced.
	Model string `json:"model,omitempty"`
}

// ModelID renders a short identifier from the model's provenance, falling back
// to the kind alone.
func (m *Model) ModelID() string {
	if m == nil {
		return ""
	}
	if v, ok := m.Provenance["id"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if v, ok := m.Provenance["run_id"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return string(m.Kind) + "@" + s
		}
	}
	return string(m.Kind)
}

// Score evaluates the model against one feature vector, resolving columns by
// name. schemaVersion must match the version the model was fitted on.
//
// Every feature the model needs must be present; a missing one is an error
// rather than an imputed zero. Zero is a legitimate value for most of these
// columns, so imputing it would not degrade the score visibly - it would just
// make it wrong, which is the failure mode a gate can least afford.
func (m *Model) Score(schemaVersion int, names []string, values []float64) (Prediction, error) {
	if m == nil {
		return Prediction{}, fmt.Errorf("predictor: no model loaded")
	}
	if len(names) != len(values) {
		return Prediction{}, fmt.Errorf(
			"predictor: feature vector has %d names and %d values", len(names), len(values))
	}
	if schemaVersion != m.FeatureSchemaVersion {
		return Prediction{}, fmt.Errorf(
			"predictor: feature schema version %d does not match the model's %d; "+
				"retrain and re-export rather than scoring across schemas",
			schemaVersion, m.FeatureSchemaVersion)
	}
	x, err := m.resolve(names, values)
	if err != nil {
		return Prediction{}, err
	}

	var score float64
	switch m.Kind {
	case RandomForest:
		if score, err = m.forestScore(x); err != nil {
			return Prediction{}, err
		}
	default:
		score = m.linearScore(x)
	}
	return Prediction{
		Score:     score,
		Translate: score >= m.Threshold,
		Threshold: m.Threshold,
		Model:     m.ModelID(),
	}, nil
}

// resolve returns the model's features in model order, or names every column
// the vector lacks.
func (m *Model) resolve(names []string, values []float64) ([]float64, error) {
	index := make(map[string]int, len(names))
	for i, n := range names {
		index[n] = i
	}
	x := make([]float64, len(m.Features))
	var missing []string
	for i, name := range m.Features {
		j, ok := index[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		x[i] = values[j]
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf(
			"predictor: feature vector is missing %d column(s) the model needs: %s",
			len(missing), strings.Join(missing, ", "))
	}
	return x, nil
}

func (m *Model) linearScore(x []float64) float64 {
	z := m.Intercept
	for i, v := range x {
		// A zero scale means the training corpus had no variance in this
		// column. StandardScaler emits 1.0 in that case and sklearn's
		// VarianceThreshold normally removes such columns first, so this is
		// defensive: contribute nothing rather than divide by zero.
		if m.Scale[i] != 0 {
			v = (v - m.Mean[i]) / m.Scale[i]
		} else {
			v = 0
		}
		z += v * m.Coefficients[i]
	}
	return sigmoid(z)
}

// forestScore is predict_proba's positive column: the mean over trees of the
// leaf value each tree assigns.
func (m *Model) forestScore(x []float64) (float64, error) {
	for i, v := range x {
		// scikit-learn refuses NaN and values beyond float32's range before it
		// walks a tree. Here a NaN would compare false at every split and walk
		// silently right; neither is a score for the function.
		if math.IsNaN(v) || math.Abs(v) > math.MaxFloat32 {
			return 0, fmt.Errorf("predictor: feature %q is %v, which a random forest cannot score",
				m.Features[i], v)
		}
	}
	sum := 0.0
	for i := range m.Trees {
		sum += m.Trees[i].leafValue(x)
	}
	return sum / float64(len(m.Trees)), nil
}

func (t *Tree) leafValue(x []float64) float64 {
	node := 0
	for t.Left[node] != leaf {
		// scikit-learn casts the input to float32 before walking a tree and
		// compares that against a float64 threshold. Comparing the float64 value
		// directly would disagree whenever a value and a threshold round to the
		// same float32 - rare, silent, and always exactly at a split.
		if float64(float32(x[t.Feature[node]])) <= t.Threshold[node] {
			node = t.Left[node]
		} else {
			node = t.Right[node]
		}
	}
	return t.Value[node]
}

// sigmoid is written to stay stable for large |z|: the naive form overflows to
// +Inf in the exponent for strongly negative z, which turns a confident "skip"
// into a NaN.
func sigmoid(z float64) float64 {
	if z >= 0 {
		return 1 / (1 + math.Exp(-z))
	}
	e := math.Exp(z)
	return e / (1 + e)
}
