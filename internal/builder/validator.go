package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/adrg/strutil"
	"github.com/adrg/strutil/metrics"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	log "github.com/sirupsen/logrus"
)

// GoPackageTester runs the package in the working directory and validates its
// stdout against expected test outputs.
type GoPackageTester struct {
	validator ValidationStrategy
}

func init() {
	pipeline.RegisterConverterFactory("goTester", NewGoPackageTester)
}

// NewGoPackageTester constructs a tester with an optional validation strategy
// from args.
func NewGoPackageTester(args map[string]interface{}) pipeline.Converter {
	var validator ValidationStrategy
	if kind, ok := args["strategy"].(string); ok {
		switch kind {
		case "json":
			validator = MakeAwareSimilarityValidation(0.85)
		default:
			validator = &SimilarityValidation{}
		}
	}
	return &GoPackageTester{
		validator: validator,
	}
}

// Apply builds/runs tests in the runner's working directory and updates the
// request's metrics and error state.
func (cc *GoPackageTester) Apply(runner *pipeline.Runner, request *domain.ConversionRequest) error {
	if request.WorkingPackage == nil {
		log.Errorf("missing working package for %s", request.Id)
		return fmt.Errorf("the working package is required")
	}

	if request.SourcePackage != nil && (len(request.SourcePackage.TestFiles)) > len(request.WorkingPackage.TestFiles) {
		request.WorkingPackage.TestFiles = make(map[string]string)
		maps.Copy(request.WorkingPackage.TestFiles, request.SourcePackage.TestFiles)
		log.Debugf("Recoverting WP Tests")
	}

	startTime := time.Now()
	errCount := 0
	ctx := runner
	log.Debugf("Running GoPackageTester with %d tests", len(request.WorkingPackage.TestFiles))
	for testFile, err := range maps.Collect(request.WorkingPackage.GetTestFiles()) {
		if request.Metrics != nil {
			request.Metrics.TestCases[testFile.Name] = false
		}
		if err != nil {
			log.Debugf("failed to read test %s: %+v", testFile.Name, err)
			errCount++
			continue
		}

		success, err := cc.doTest(ctx, runner.WorkingDir(), testFile)
		if err != nil {
			errCount++
			log.Debugf("test %s failed: %v", testFile.Name, err)
			continue
		}
		if !success {
			errCount++
			log.Debugf("test %s failed: %v", testFile.Name, err)
			continue
		}
		if request.Metrics != nil {
			request.Metrics.TestCases[testFile.Name] = true
		}
		log.Debugf("test %s succeeded ", testFile.Name)
	}
	if request.Metrics != nil {
		request.Metrics.TestTime = time.Since(startTime)
		request.Metrics.TestError = errCount
	}
	if errCount != 0 {
		log.Debugf("tests failed: %d/%d", errCount, len(request.WorkingPackage.TestFiles))
		return domain.NewTestingError(fmt.Errorf("%d tests failed", errCount), errCount)
	}
	log.Debugf("%d tests succeeded", len(request.WorkingPackage.TestFiles))
	return nil
}

func (cc *GoPackageTester) doTest(ctx context.Context, dir string, t *domain.TestFile) (bool, error) {
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), t.Env...)
	in := strings.NewReader(t.Input)
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = errBuf
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("test failed. %s - %s - %s", out.String(), errBuf.String(), err)
	}
	cleanOut := domain.MinimizeString(out.String())

	assertEquals := cc.validateTestOutput(ctx, cleanOut, t)
	if !assertEquals {
		log.Debugf("test failed. %s, expected:%s, errors:%s", cleanOut, t.Output, errBuf.String())
		return false, fmt.Errorf("test failed. %s, expected:%s, errors:%s", cleanOut, t.Output, errBuf.String())
	}

	return true, nil
}

func (cc *GoPackageTester) validateTestOutput(ctx context.Context, testOutput string, testFile *domain.TestFile) bool {
	validator := cc.validator
	if validator == nil {
		validator = &SimilarityValidation{}
	}

	if testFile.UndeterministicResults {
		return validator.validateUndeterministic(testOutput, testFile.Output)
	}
	return validator.validate(testOutput, testFile.Output)
}

type ValidationStrategy interface {
	validate(in, expected string) bool
	validateUndeterministic(in, expected string) bool
}

type SimilarityValidation struct{}

// validate passes when the output is sufficiently similar to the expected
// value (overlap coefficient: 1.0 = identical, 0.0 = disjoint).
func (SimilarityValidation) validate(in, expected string) bool {
	sim := strutil.Similarity(in, expected, metrics.NewOverlapCoefficient())
	return sim >= 0.9
}

func (SimilarityValidation) validateUndeterministic(in, expected string) bool {
	sim := strutil.Similarity(in, expected, metrics.NewOverlapCoefficient())
	return sim >= 0.6
}

// MakeAwareSimilarityValidation returns a JSON-aware validation strategy.
func MakeAwareSimilarityValidation(threshold float64) ValidationStrategy {
	return &JsonAwareSimilarityValidation{
		valueValidation:    true,
		threshold:          threshold,
		fallBackValidation: SimilarityValidation{},
	}
}

// ValidateAwareSimilarity evaluates JSON-aware similarity with the given threshold.
func ValidateAwareSimilarity(threshold float64, actual, expected string) bool {
	validator := MakeAwareSimilarityValidation(threshold)
	return validator.validate(actual, expected)
}

type JsonAwareSimilarityValidation struct {
	valueValidation    bool
	threshold          float64
	fallBackValidation ValidationStrategy
}

func (vs *JsonAwareSimilarityValidation) validate(in, expected string) bool {
	var expectedJSON map[string]interface{}
	if err := json.Unmarshal([]byte(expected), &expectedJSON); err != nil {
		return vs.fallBackValidation.validate(in, expected)
	}

	var actualJSON map[string]interface{}
	if err := json.Unmarshal([]byte(in), &actualJSON); err != nil {
		return vs.fallBackValidation.validate(in, expected)
	}

	if val, ok := actualJSON["error"]; ok {
		log.Debugf("handle function caused error: %s", val)
		return false
	}

	if val, ok := actualJSON["response"]; ok {
		respMap, ok := val.(map[string]interface{})
		if !ok {
			// the handler returned a non-object response while an object was
			// expected: treat it as a mismatch instead of panicking the run.
			return false
		}
		return vs.compareMap(expectedJSON, respMap)
	}
	return vs.compareMap(expectedJSON, actualJSON)
}

func (vs *JsonAwareSimilarityValidation) validateUndeterministic(in, expected string) bool {
	valueValidation := vs.valueValidation
	vs.valueValidation = false
	result := vs.validate(in, expected)
	vs.valueValidation = valueValidation
	return result
}

func (vs *JsonAwareSimilarityValidation) compareSimple(v, vv any) bool {
	switch expected := v.(type) {
	case string:
		actual, ok := vv.(string)
		if !ok {
			// expected/actual leaf types differ (e.g. "200" vs 200): a
			// mismatch when values are validated, ignored otherwise -
			// never a panic that aborts the whole conversion.
			return !vs.valueValidation
		}
		if strings.HasPrefix(expected, "{") && strings.HasSuffix(expected, "}") && strings.HasPrefix(actual, "{") && strings.HasSuffix(actual, "}") {
			var expectedValue map[string]interface{}
			var actualValue map[string]interface{}
			if json.Unmarshal([]byte(expected), &expectedValue) == nil &&
				json.Unmarshal([]byte(actual), &actualValue) == nil {
				log.Debugf("found two json strings, comparing as structs")
				return vs.compareMap(expectedValue, actualValue)
			}
			return vs.fallback(expected, actual)
		}
		log.Debugf("found two strings, comparing as strings")
		if !vs.fallback(expected, actual) {
			return false
		}
	case float64:
		// JSON numbers always decode to float64, so this covers ints too.
		if !vs.valueValidation {
			break
		}
		actual, ok := vv.(float64)
		if !ok {
			return false
		}
		if expected != actual {
			return false
		}
	}
	return true
}

func (vs *JsonAwareSimilarityValidation) fallback(exp, act string) bool {
	if !vs.valueValidation {
		return true
	}
	sim := strutil.Similarity(exp, act, metrics.NewOverlapCoefficient())
	if sim < vs.threshold {
		return false
	}
	return true
}

func (vs *JsonAwareSimilarityValidation) compareMap(expected, actual map[string]interface{}) bool {
	for k, v := range expected {
		vv, ok := actual[k]
		if !ok {
			return false
		}
		switch ev := v.(type) {
		case map[string]interface{}:
			switch av := vv.(type) {
			case map[string]interface{}:
				log.Debugf("found two json objects, comparing as structs")
				// keep iterating the remaining keys after a nested match -
				// returning here would silently accept all sibling keys.
				if !vs.compareMap(ev, av) {
					return false
				}
			case string:
				log.Debugf("comparing an object to a string, by assuming the string is json.")
				if strings.HasPrefix(av, "{") && strings.HasSuffix(av, "}") {
					var actualData map[string]interface{}
					if err := json.Unmarshal([]byte(av), &actualData); err != nil {
						return false
					}
					if !vs.compareMap(ev, actualData) {
						return false
					}
				} else {
					data, _ := json.Marshal(ev)
					if !vs.fallback(string(data), av) {
						return false
					}
				}
			default:
				return false
			}
		case []interface{}:
			av, ok := vv.([]interface{})
			if !ok {
				return false
			}
			if len(ev) != len(av) {
				return false
			}
			for i, vEl := range ev {
				if !vs.compareSimple(vEl, av[i]) {
					return false
				}
			}
		default:
			if !vs.compareSimple(v, vv) {
				return false
			}
		}
	}
	return true
}
