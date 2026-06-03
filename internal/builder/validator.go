package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/strutil"
	"github.com/adrg/strutil/metrics"
	"github.com/carl-egge/ise-predictive-refaas/internal/awsenv"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	log "github.com/sirupsen/logrus"
)

// GoPackageTester runs the package in the working directory and validates its
// stdout against expected test outputs.
type GoPackageTester struct {
	validator     ValidationStrategy
	flociEnabled  bool
	flociEndpoint string
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
	flociEnabled := getBoolArg(args, "floci_enabled")
	flociEndpoint := getStringArg(args, "floci_endpoint")
	return &GoPackageTester{
		validator:     validator,
		flociEnabled:  flociEnabled,
		flociEndpoint: flociEndpoint,
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
		if isFlociTestFile(testFile.Name) {
			log.Debugf("skipping floci test file %s", testFile.Name)
			continue
		}
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

func isFlociTestFile(name string) bool {
	clean := filepath.ToSlash(name)
	if strings.HasPrefix(clean, "test/floci/") {
		return true
	}
	return strings.HasSuffix(clean, ".floci.json") ||
		strings.HasSuffix(clean, ".floci.yaml") ||
		strings.HasSuffix(clean, ".floci.yml")
}

func (cc *GoPackageTester) doTest(ctx context.Context, dir string, t *domain.TestFile) (bool, error) {
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	envMap := awsenv.MergeEnv(os.Environ(), t.Env)
	endpoint := ""
	if cc.flociEnabled && cc.flociEndpoint != "" {
		endpoint = awsenv.NormalizeEndpoint(cc.flociEndpoint)
	}
	envMap = awsenv.Augment(envMap, endpoint)
	cmd.Env = awsenv.FlattenEnv(envMap)
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

func (SimilarityValidation) validate(in, expected string) bool {
	sim := strutil.Similarity(in, expected, metrics.NewOverlapCoefficient())
	return sim < 0.9
}

func (SimilarityValidation) validateUndeterministic(in, expected string) bool {
	sim := strutil.Similarity(in, expected, metrics.NewOverlapCoefficient())
	return sim < 0.6
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
		return vs.compareMap(expectedJSON, val.(map[string]interface{}))
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
	switch v.(type) {
	case string:
		if strings.HasPrefix(v.(string), "{") && strings.HasSuffix(v.(string), "}") && strings.HasPrefix(vv.(string), "{") && strings.HasSuffix(vv.(string), "}") {
			var expectedValue map[string]interface{}
			var actualValue map[string]interface{}

			var err error
			err = json.Unmarshal([]byte(v.(string)), &expectedValue)
			if err != nil {
				if !vs.fallback(v.(string), vv.(string)) {
					return false
				}
			}
			err = json.Unmarshal([]byte(vv.(string)), &actualValue)
			if err != nil {
				if !vs.fallback(v.(string), vv.(string)) {
					return false
				}
			}
			log.Debugf("found two json strings, comparing as structs")
			return vs.compareMap(expectedValue, actualValue)
		}
		log.Debugf("found two strings, comparing as strings")
		if !vs.fallback(v.(string), vv.(string)) {
			return false
		}
	case int:
		if !vs.valueValidation {
			break
		}
		if v.(int) != vv.(int) {
			return false
		}
	case float64:
		if !vs.valueValidation {
			break
		}
		if v.(float64) != vv.(float64) {
			return false
		}
	}
	return true
}

func getStringArg(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case string:
			return v
		case fmt.Stringer:
			return v.String()
		}
	}
	return ""
}

func getBoolArg(args map[string]interface{}, key string) bool {
	if args == nil {
		return false
	}
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			trimmed := strings.ToLower(strings.TrimSpace(v))
			return trimmed == "true" || trimmed == "1" || trimmed == "yes"
		}
	}
	return false
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
		if vv, ok := actual[k]; ok {
			switch v.(type) {
			case map[string]interface{}:
				switch vv.(type) {
				case map[string]interface{}:
					log.Debugf("found two json objects, comparing as structs")
					return vs.compareMap(v.(map[string]interface{}), vv.(map[string]interface{}))
				case string:
					log.Debugf("comparing an object to a string, by assuming the string is json.")
					if strings.HasPrefix(vv.(string), "{") && strings.HasSuffix(vv.(string), "}") {
						var actualData map[string]interface{}
						if err := json.Unmarshal([]byte(vv.(string)), &actualData); err != nil {
							return false
						}
						return vs.compareMap(v.(map[string]interface{}), actualData)
					}
					data, _ := json.Marshal(v.(map[string]interface{}))
					if !vs.fallback(string(data), vv.(string)) {
						return false
					}
				default:
					return false
				}
			case []interface{}:
				switch vv.(type) {
				case []interface{}:
					if len(v.([]interface{})) != len(vv.([]interface{})) {
						return false
					}
					for i, vEl := range v.([]interface{}) {
						vvEl := vv.([]interface{})[i]
						if !vs.compareSimple(vEl, vvEl) {
							return false
						}
					}
				default:
					return false
				}
			default:
				if !vs.compareSimple(v, vv) {
					return false
				}
			}
		} else {
			return false
		}
	}
	return true
}
