// Package predictor scores an uploaded function's ex-ante feature vector and
// turns the score into a translate/skip decision ([I10]).
//
// It is deliberately a *reader*, not a trainer. The model is fitted offline in
// evaluation/prediction (scikit-learn) and exported as JSON; everything here is
// a dot product over standardized features. That keeps go.mod free of any ML
// dependency — the same separation cmd/energy keeps by holding its constants in
// evaluation/energy.config.json rather than in code — and it means the shipped
// artifact is a vector of coefficients an examiner can read.
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

// Kind is the model family an exported file describes. Only logistic
// regression is implemented: [I7] measured it as the arm that both transfers
// to a second corpus (AUC 0.850 vs the forest's 0.525) and stays above chance
// on the most expensive complexity bucket, and it is the arm whose output is a
// calibrated probability rather than a vote fraction.
type Kind string

const LogisticRegression Kind = "logistic_regression"

// Model is the exported classifier: a standardizer and a linear score.
//
// Features names the columns this model was fitted on, in coefficient order.
// It is not required to be the whole feature vector — the training pipeline
// drops zero-variance columns, and on the evaluation_set corpus 8 of 56 carry
// no information at all — so scoring resolves each name against the supplied
// vector rather than assuming positional alignment.
type Model struct {
	Kind Kind `json:"model"`

	// FeatureSchemaVersion is pyscan.FeatureSchemaVersion as it stood when
	// the model was fitted. Scoring refuses a mismatch: a vector recorded
	// under a different schema may have the same width and different
	// meanings, which would feed the wrong number into the wrong coefficient
	// and produce a confident, wrong answer rather than an error.
	FeatureSchemaVersion int `json:"feature_schema_version"`

	Features     []string  `json:"features"`
	Mean         []float64 `json:"mean"`
	Scale        []float64 `json:"scale"`
	Coefficients []float64 `json:"coefficients"`
	Intercept    float64   `json:"intercept"`

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
	if m.Kind != LogisticRegression {
		return fmt.Errorf("predictor: unsupported model kind %q (only %q is implemented)",
			m.Kind, LogisticRegression)
	}
	if len(m.Features) == 0 {
		return fmt.Errorf("predictor: model names no features")
	}
	n := len(m.Features)
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

// Prediction is one scored function.
type Prediction struct {
	// Score is P(the pipeline translates this function successfully), as a
	// calibrated probability from the logistic model.
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
// columns, so imputing it would not degrade the score visibly — it would just
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

	index := make(map[string]int, len(names))
	for i, n := range names {
		index[n] = i
	}

	z := m.Intercept
	var missing []string
	for i, name := range m.Features {
		j, ok := index[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		x := values[j]
		// A zero scale means the training corpus had no variance in this
		// column. StandardScaler emits 1.0 in that case and sklearn's
		// VarianceThreshold normally removes such columns first, so this is
		// defensive: contribute nothing rather than divide by zero.
		if m.Scale[i] != 0 {
			x = (x - m.Mean[i]) / m.Scale[i]
		} else {
			x = 0
		}
		z += x * m.Coefficients[i]
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Prediction{}, fmt.Errorf(
			"predictor: feature vector is missing %d column(s) the model needs: %s",
			len(missing), strings.Join(missing, ", "))
	}

	score := sigmoid(z)
	return Prediction{
		Score:     score,
		Translate: score >= m.Threshold,
		Threshold: m.Threshold,
		Model:     m.ModelID(),
	}, nil
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
