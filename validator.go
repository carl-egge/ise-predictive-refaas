// Package main implements test and validation helpers used to verify the
// correctness of converted Go packages.
package main

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
	log "github.com/sirupsen/logrus"
)

// GoPackageTester runs the package in the working directory and
// validates its stdout against expected test outputs.
type GoPackageTester struct {
	validator ValidationStrategy
}

// makeGoPackageTester constructs a tester with an optional validation
// strategy from `args`.
func makeGoPackageTester(args map[string]interface{}) Converter {
	var validator ValidationStrategy
	if kind, ok := args["strategy"].(string); ok {
		switch kind {
		case "json":
			validator = MakeAwareSimilarityValidation(0.85)
			break
		default:
			validator = &SimilarityValidation{}
			break
		}
	}
	return &GoPackageTester{
		validator: validator,
	}
}

// Apply builds/runs tests in the runner's `WorkingDir` and updates
// the request's metrics and error state.
func (cc *GoPackageTester) Apply(runner *PipelineRunner, request *ConversionRequest) error {
	if request.WorkingPackage == nil {
		log.Errorf("missing working package for %s", request.Id)
		return fmt.Errorf("the working package is required")
	}

	if request.SourcePackage != nil && (len(request.SourcePackage.TestFiles)) > len(request.WorkingPackage.TestFiles) {
		request.WorkingPackage.TestFiles = make(map[string]string)
		maps.Copy(request.WorkingPackage.TestFiles, request.SourcePackage.TestFiles)
		log.Debugf("Recoverting WP Tests")
	}

	start_time := time.Now()
	err_cnt := 0
	ctx := runner
	log.Debugf("Running GoPackageTester with %d tests", len(request.WorkingPackage.TestFiles))
	for testfile, err := range maps.Collect(request.WorkingPackage.getTestFiles()) {

		request.Metrics.TestCases[testfile.Name] = false
		if err != nil {
			log.Debugf("failed to read test %s: %+v", testfile.Name, err)
			err_cnt++
			continue
		}

		success, err := cc.doTest(ctx, runner.WorkingDir, testfile)
		if err != nil {
			err_cnt++
			log.Debugf("test %s failed: %v", testfile.Name, err)
			continue
		}
		if !success {
			err_cnt++
			log.Debugf("test %s failed: %v", testfile.Name, err)
			continue
		}
		request.Metrics.TestCases[testfile.Name] = true
		log.Debugf("test %s succeeded ", testfile.Name)
	}
	request.Metrics.TestTime = time.Since(start_time)
	request.Metrics.TestError = err_cnt
	if err_cnt != 0 {
		log.Debugf("tests failed: %d/%d", err_cnt, len(request.WorkingPackage.TestFiles))
		return TestingError{fmt.Errorf("%d tests failed", err_cnt), err_cnt}
	}
	log.Debugf("%d tests succeeded", len(request.WorkingPackage.TestFiles))
	return nil
}

func (cc *GoPackageTester) doTest(ctx context.Context, dir string, t *TestFile) (bool, error) {
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), t.Env...)
	_in := strings.NewReader(t.Input)
	_out := &bytes.Buffer{}
	_err := &bytes.Buffer{}

	cmd.Stdin = _in
	cmd.Stdout = _out
	cmd.Stderr = _err
	err := cmd.Run()
	if err != nil {
		return false, fmt.Errorf("test failed. %s - %s - %s", _out.String(), _err.String(), err)
	}
	cleanOut := MinimizeString(_out.String())

	assertEquals := cc.validateTestOutput(ctx, cleanOut, t)
	if !assertEquals {
		log.Debugf("test failed. %s, expected:%s, errors:%s", cleanOut, t.Output, _err.String())
		return false, fmt.Errorf("test failed. %s, expected:%s, errors:%s", cleanOut, t.Output, _err.String())
	}

	return true, nil
}

func (cc *GoPackageTester) validateTestOutput(ctx context.Context, testOutput string, testFile *TestFile) bool {
	validator := cc.validator
	if validator == nil {
		validator = &SimilarityValidation{}
	}

	if testFile.UndeterministicResults {
		return validator.validateUndeterministic(testOutput, testFile.Output)
	} else {
		return validator.validate(testOutput, testFile.Output)
	}

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

func (s SimilarityValidation) validateUndeterministic(in, expected string) bool {
	sim := strutil.Similarity(in, expected, metrics.NewOverlapCoefficient())
	return sim < 0.6
}

func MakeAwareSimilarityValidation(threshold float64) ValidationStrategy {
	return &JsonAwareSimilarityValidation{
		valueValidation:    true,
		threshold:          threshold,
		fallBackValidation: SimilarityValidation{},
	}
}

type JsonAwareSimilarityValidation struct {
	valueValidation    bool
	threshold          float64
	fallBackValidation ValidationStrategy
}

func (vs *JsonAwareSimilarityValidation) validate(in, expected string) bool {
	var expectedJson map[string]interface{}
	err := json.Unmarshal([]byte(expected), &expectedJson)
	if err != nil {
		return vs.fallBackValidation.validate(in, expected)
	}

	var actualJson map[string]interface{}

	err = json.Unmarshal([]byte(in), &actualJson)
	if err != nil {
		return vs.fallBackValidation.validate(in, expected)
	}

	if val, ok := actualJson["error"]; ok {
		log.Debugf("handle function caused error: %s", val)
		return false
	}

	if val, ok := actualJson["response"]; ok {
		return vs.compareMap(expectedJson, val.(map[string]interface{}))
	} else {
		return vs.compareMap(expectedJson, actualJson)
	}
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

			var expected_value map[string]interface{}
			var actual_value map[string]interface{}

			var err error
			err = json.Unmarshal([]byte(v.(string)), &expected_value)
			if err != nil {
				if !vs.fallback(v.(string), vv.(string)) {
					return false
				}
			}
			err = json.Unmarshal([]byte(vv.(string)), &actual_value)
			if err != nil {
				if !vs.fallback(v.(string), vv.(string)) {
					return false
				}
			}
			log.Debugf("found two json strings, comparing as structs")
			return vs.compareMap(expected_value, actual_value)
		} else {
			log.Debugf("found two strings, comparing as strings")
			if !vs.fallback(v.(string), vv.(string)) {
				return false
			}
		}
		break
	case int:
		if !vs.valueValidation {
			break
		}
		if v.(int) != vv.(int) {
			return false
		}
		break
	case float64:
		if !vs.valueValidation {
			break
		}
		if v.(float64) != vv.(float64) {
			return false
		}
		break
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
						var actual_data map[string]interface{}
						err := json.Unmarshal([]byte(vv.(string)), &actual_data)
						if err != nil {
							return false
						}
						return vs.compareMap(v.(map[string]interface{}), actual_data)
					} else {
						data, _ := json.Marshal(v.(map[string]interface{}))
						if !vs.fallback(string(data), vv.(string)) {
							return false
						}
					}
					break
				default:
					return false
				}

			case []interface{}:
				switch vv.(type) {
				case []interface{}:
					if len(v.([]interface{})) != len(vv.([]interface{})) {
						return false
					}
					for i, v_el := range v.([]interface{}) {
						vv_el := vv.([]interface{})[i]
						if !vs.compareSimple(v_el, vv_el) {
							return false
						}
					}
					break
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
