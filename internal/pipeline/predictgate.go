package pipeline

import (
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
	"github.com/carl-egge/ise-predictive-refaas/internal/predictor"
	"github.com/carl-egge/ise-predictive-refaas/internal/pyscan"
	log "github.com/sirupsen/logrus"
)

func init() {
	RegisterConverterFactory("predictGate", NewPredictGateConverter)
}

// PredictGateConverter scores a function's ex-ante feature vector before any
// LLM call is made, and — when configured to enforce — declines to translate
// candidates it scores below the operating point ([I10]).
//
// It reads the vector the pyScan stage already recorded on req.Metrics rather
// than scanning again, which is what makes the gate's marginal cost the
// inference alone: [I8] measured feature extraction at 27 ms and inference at
// 0.9 µs, so a gate placed after pyScan costs ~10⁻⁷ of a translation, while one
// that re-scanned would cost ~10⁻³. Ordering is therefore not a style choice —
// `pyScan` must be an ancestor of this task in the graph.
//
// Three deliberate properties:
//
//   - **Off by default.** Both the stage being present in a pipeline and the
//     Runner having Predict.Enabled set are required. A pipeline with an active
//     gate has a different denominator from every baseline recorded in section
//     H, so enabling it must be an explicit act.
//   - **It scores even when it does not enforce.** With Enforce false the score
//     is recorded and the job proceeds untouched. That configuration is how a
//     deployment accumulates (score → outcome) rows, which is the only cheap
//     answer to the N = 95 problem over time.
//   - **A missing model or a missing vector does not silently pass.** Unlike
//     pyScan — which enriches a prompt and can degrade to a warning — this stage
//     decides whether work happens. Failing open would report "translated
//     everything" while claiming a gate was active. So a configured-but-broken
//     gate is an error, and only an explicitly disabled one is a no-op.
type PredictGateConverter struct {
	// modelPath overrides the Runner's configured model for this task. Rarely
	// useful, but it is what lets one pipeline compare two exported models.
	modelPath string
	// enforce, when set on the task, overrides the Runner's Enforce flag.
	enforce *bool
}

// NewPredictGateConverter builds the stage. task_args:
//
//	model:   path to an exported model JSON (default: the Runner's Predict.ModelPath)
//	enforce: true|false            (default: the Runner's Predict.Enforce)
func NewPredictGateConverter(args map[string]interface{}) Converter {
	c := &PredictGateConverter{}
	if v, ok := args["model"]; ok {
		if s, ok := v.(string); ok {
			c.modelPath = s
		}
	}
	if v, ok := args["enforce"]; ok {
		var b bool
		switch t := v.(type) {
		case bool:
			b = t
		case string:
			parsed, err := strconv.ParseBool(t)
			if err != nil {
				log.Warnf("predictGate: ignoring unparseable enforce=%q", t)
				break
			}
			b = parsed
		}
		c.enforce = &b
	}
	return c
}

// Apply scores the request and, when enforcing, aborts it with a
// domain.PredictionSkip.
func (c *PredictGateConverter) Apply(runner *Runner, req *domain.ConversionRequest) error {
	cfg := runner.PredictConfiguration()
	if !cfg.Enabled {
		log.Debugf("predictGate: disabled, passing through")
		return nil
	}
	if c.modelPath != "" {
		cfg.ModelPath = c.modelPath
	}
	if c.enforce != nil {
		cfg.Enforce = *c.enforce
	}

	features := req.Metrics.GetFeatures()
	if features == nil {
		return fmt.Errorf("predictGate: no feature vector on this job - " +
			"the pyScan stage must run before the gate, and with task_args.required " +
			"set so a failed scan cannot reach here silently")
	}

	prediction, err := scoreVector(cfg, features)
	if err != nil {
		return err
	}

	skipped := cfg.Enforce && !prediction.Translate

	// Recorded before the decision is acted on, so a skipped job still carries
	// its score into the run log.
	req.Metrics.RecordPrediction(prediction.Score, prediction.Threshold,
		prediction.Translate, skipped, prediction.Model)

	if skipped {
		log.Infof("predictGate: declining %s (score %.3f < threshold %.3f)",
			req.Metrics.FunctionID, prediction.Score, prediction.Threshold)
		return domain.NewPredictionSkip(prediction.Score, prediction.Threshold, prediction.Model)
	}
	if !prediction.Translate {
		log.Infof("predictGate: %s scored %.3f (below threshold %.3f) but enforcement "+
			"is off; translating anyway",
			req.Metrics.FunctionID, prediction.Score, prediction.Threshold)
	} else {
		log.Debugf("predictGate: %s scored %.3f (threshold %.3f)",
			req.Metrics.FunctionID, prediction.Score, prediction.Threshold)
	}
	return nil
}

// ScorePackage scans a package and scores it exactly as the gate would, without
// translating anything — the POST /predict path.
//
// It shares scoreVector and loadModel with Apply on purpose: an endpoint that
// answered "what would the gate do?" through its own copy of the arithmetic
// would eventually answer something else, and the whole value of the endpoint
// is that its answer is the one the pipeline will act on.
//
// The returned feature vector is the one that produced the score, so a caller
// can report or store it alongside.
func (cc *Runner) ScorePackage(pkg *domain.DeploymentPackage) (predictor.Prediction, *domain.FeatureVector, error) {
	cfg := cc.PredictConfiguration()
	if !cfg.Enabled {
		return predictor.Prediction{}, nil, fmt.Errorf(
			"prediction is not enabled on this service (set predict.enabled or PREDICT_ENABLED)")
	}
	if pkg == nil || pkg.RootFile == "" {
		return predictor.Prediction{}, nil, fmt.Errorf("no source root file to scan")
	}

	result, err := pyscan.Scan(cc.ctx(), pkg.RootFile)
	if err != nil {
		return predictor.Prediction{}, nil, fmt.Errorf("python analysis unavailable: %w", err)
	}
	cases, err := fixture.FromPackage(pkg)
	if err != nil {
		// Uploads are validated before they reach here, so this is unexpected;
		// the fixture-derived feature family would be silently zeroed, which
		// would change the score, so refuse rather than answer wrongly.
		return predictor.Prediction{}, nil, fmt.Errorf("fixtures unreadable: %w", err)
	}
	features, err := pyscan.BuildFeatures(result, cases)
	if err != nil {
		return predictor.Prediction{}, nil, err
	}

	fv := &domain.FeatureVector{
		SchemaVersion: features.SchemaVersion,
		Names:         features.Names,
		Values:        features.Values,
	}
	prediction, err := scoreVector(cfg, fv)
	return prediction, fv, err
}

// scoreVector loads the configured model and scores one vector, applying a
// configured threshold override after scoring so the recorded score always
// stays the model's own.
func scoreVector(cfg PredictConfig, fv *domain.FeatureVector) (predictor.Prediction, error) {
	if cfg.ModelPath == "" {
		return predictor.Prediction{}, fmt.Errorf(
			"predictGate: enabled but no model configured " +
				"(set predict.model, PREDICT_MODEL, or the task's model arg)")
	}
	model, err := loadModel(cfg.ModelPath)
	if err != nil {
		return predictor.Prediction{}, err
	}
	prediction, err := model.Score(fv.SchemaVersion, fv.Names, fv.Values)
	if err != nil {
		return predictor.Prediction{}, fmt.Errorf("predictGate: %w", err)
	}
	if cfg.Threshold != nil {
		prediction.Threshold = *cfg.Threshold
		prediction.Translate = prediction.Score >= prediction.Threshold
	}
	return prediction, nil
}

// modelCache holds parsed models. A model is a few kilobytes of coefficients
// and never changes underneath a job, so re-reading it per conversion would be
// pure syscall overhead — the same reason the llmconnector clients cache their
// transport in Configure.
//
// The key includes the file's size and modification time rather than the path
// alone, so replacing a model in place is picked up on the next job without a
// /reconfigure. That matters because the obvious failure mode of a path-keyed
// cache is silent: the service keeps scoring with the old coefficients and
// nothing in the logs says so.
var modelCache = struct {
	sync.Mutex
	entries map[string]*predictor.Model
}{entries: map[string]*predictor.Model{}}

func loadModel(path string) (*predictor.Model, error) {
	key := path
	if info, err := os.Stat(path); err == nil {
		key = fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
	}

	modelCache.Lock()
	defer modelCache.Unlock()
	if m, ok := modelCache.entries[key]; ok {
		return m, nil
	}
	m, err := predictor.LoadFile(path)
	if err != nil {
		// Deliberately not cached: a load failure is usually a fixable
		// configuration mistake, and caching it would keep failing after the
		// fix until the service restarted.
		return nil, fmt.Errorf("predictGate: %w", err)
	}
	modelCache.entries[key] = m
	log.Infof("predictGate: loaded %s (%d features, schema v%d, threshold %.3f)",
		m.ModelID(), len(m.Features), m.FeatureSchemaVersion, m.Threshold)
	return m, nil
}
