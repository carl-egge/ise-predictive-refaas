package pipeline

import (
	"strconv"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
	"github.com/carl-egge/ise-predictive-refaas/internal/pyscan"
	log "github.com/sirupsen/logrus"
)

// Metadata keys this stage writes. They become top-level prompt template
// vars (LLMConverter.Apply promotes every req.Metadata key), so a translate
// prompt can reference {{ .lib_hints }} and {{ .py_features }} directly.
const (
	MetaLibHints    = "lib_hints"
	MetaPyFeatures  = "py_features"
	MetaFeasibility = "feasibility_warning"
	MetaCC          = "py_cc"
	MetaLLOC        = "py_lloc"
)

func init() {
	RegisterConverterFactory("pyScan", NewScanConverter)
}

// ScanConverter runs the deterministic Python analysis and publishes its
// findings on the request ([C8]).
//
// It is not an LLM stage and costs no tokens: one process spawn, tens of
// milliseconds. Placed before the translate stage it turns library
// equivalence from something the model must infer into something it is told.
//
// Failure policy: this stage *enriches* a prompt, it does not gate one. A
// missing interpreter or an unparseable source degrades to a warning and an
// empty hint set rather than failing the conversion, because the translation
// is no worse off than it was before [C8] existed. The prediction module
// ([I10]) is the caller that must not tolerate a missing scan, and it checks
// Available() itself rather than relying on this stage's outcome.
type ScanConverter struct {
	// required makes a scan failure fail the task. Off by default; the
	// benchmark run for [I1] sets it so every recorded job is guaranteed to
	// carry a feature vector, which is what makes the run log usable as
	// training data without a second pass.
	required bool
}

// NewScanConverter builds the stage. task_args:
//
//	required: true  - fail the task if the scan cannot run (default false)
func NewScanConverter(args map[string]interface{}) Converter {
	c := &ScanConverter{}
	if v, ok := args["required"]; ok {
		switch t := v.(type) {
		case bool:
			c.required = t
		case string:
			c.required, _ = strconv.ParseBool(t)
		}
	}
	return c
}

// Apply scans the *source* package, never the working one: the point is to
// describe the original Python, and by the time a later stage re-enters this
// task the working package may already hold Go.
func (c *ScanConverter) Apply(runner *Runner, req *domain.ConversionRequest) error {
	pkg := req.SourcePackage
	if pkg == nil || pkg.RootFile == "" {
		return c.degrade(req, "no source root file to scan", nil)
	}

	result, err := pyscan.Scan(runner.ctx(), pkg.RootFile)
	if err != nil {
		return c.degrade(req, "python analysis unavailable", err)
	}

	cases, err := fixture.FromPackage(pkg)
	if err != nil {
		// Fixtures are validated at upload ([C6]), so this is unexpected -
		// but the source-side hints are still worth publishing without them.
		log.Warnf("pyScan: fixtures unreadable, source hints only: %v", err)
		cases = nil
	}

	c.publish(req, result)

	// The feature vector is built here rather than lazily so a malformed
	// vector surfaces during the run that produced it, not months later
	// when the dataset is assembled.
	if features, err := pyscan.BuildFeatures(result, cases); err != nil {
		log.Warnf("pyScan: feature vector unavailable: %v", err)
	} else if req.Metrics != nil {
		req.Metrics.RecordFeatures(pyscan.FeatureSchemaVersion, features.Names, features.Values)
	}

	log.Debugf("pyScan: cc=%d lloc=%d imports=%d third-party=%v",
		int(result.Metric("cc")), int(result.Metric("lloc")), len(result.Imports), result.ThirdParty)
	return nil
}

// publish writes the prompt-facing findings into req.Metadata.
func (c *ScanConverter) publish(req *domain.ConversionRequest, r *pyscan.Result) {
	if req.Metadata == nil {
		req.Metadata = make(map[string]string)
	}
	set := func(key, value string) {
		if value == "" {
			// Leave the key absent rather than empty: prompt templates test
			// it with {{ if .lib_hints }}, and an empty string is falsey
			// either way, but an absent key keeps the metadata map honest
			// about what was actually found.
			delete(req.Metadata, key)
			return
		}
		req.Metadata[key] = value
	}
	set(MetaLibHints, r.LibHints())
	set(MetaPyFeatures, r.PyFeatures())
	set(MetaFeasibility, r.FeasibilityWarning())
	set(MetaCC, strconv.Itoa(int(r.Metric("cc"))))
	set(MetaLLOC, strconv.Itoa(int(r.Metric("lloc"))))

	if warning := r.FeasibilityWarning(); warning != "" {
		log.Warnf("pyScan: feasibility warning: %s", warning)
	}
}

// degrade reports a scan that could not run. Returns an error only when the
// stage was configured as required.
func (c *ScanConverter) degrade(req *domain.ConversionRequest, msg string, cause error) error {
	if cause != nil {
		log.Warnf("pyScan: %s: %v", msg, cause)
	} else {
		log.Warnf("pyScan: %s", msg)
	}
	if !c.required {
		return nil
	}
	if cause != nil {
		return cause
	}
	return errRequired(msg)
}

type errRequired string

func (e errRequired) Error() string { return "pyScan: " + string(e) }
