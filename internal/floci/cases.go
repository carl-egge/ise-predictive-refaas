package floci

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// TestSuite groups one or more test cases for a Lambda function.
type TestSuite struct {
	Name         string                  `json:"name" yaml:"name"`
	Description  string                  `json:"description" yaml:"description"`
	FunctionName string                  `json:"function_name" yaml:"function_name"`
	Setup        []SetupActionDefinition `json:"setup" yaml:"setup"`
	Cases        []TestCase              `json:"cases" yaml:"cases"`
}

// TestCase defines a single Lambda invocation and expected behavior.
type TestCase struct {
	Name        string                  `json:"name" yaml:"name"`
	Description string                  `json:"description" yaml:"description"`
	Payload     any                     `json:"payload" yaml:"payload"`
	Expected    any                     `json:"expected" yaml:"expected"`
	Env         map[string]string       `json:"env" yaml:"env"`
	Setup       []SetupActionDefinition `json:"setup" yaml:"setup"`
	SideEffects []SideEffectAssertion   `json:"side_effects" yaml:"side_effects"`
}

// SetupActionDefinition describes a pre-test action to run against Floci.
type SetupActionDefinition struct {
	Type   string         `json:"type" yaml:"type"`
	Params map[string]any `json:"params" yaml:"params"`
}

// SideEffectAssertion describes a post-invocation resource check.
type SideEffectAssertion struct {
	Type   string         `json:"type" yaml:"type"`
	Params map[string]any `json:"params" yaml:"params"`
}

// LoadSuites parses all Floci test suites from the provided test files.
func LoadSuites(testFiles map[string]string, cfg Config) ([]TestSuite, error) {
	selected := filterFlociTestFiles(testFiles, cfg)
	if len(selected) == 0 {
		return nil, nil
	}

	var suites []TestSuite
	for name, content := range selected {
		parsed, err := parseSuites(name, []byte(content))
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", name, err)
		}
		suites = append(suites, parsed...)
	}
	return suites, nil
}

func filterFlociTestFiles(testFiles map[string]string, cfg Config) map[string]string {
	selected := make(map[string]string)
	for name, content := range testFiles {
		if isFlociTestFile(name, cfg) {
			selected[name] = content
		}
	}
	return selected
}

func isFlociTestFile(name string, cfg Config) bool {
	clean := filepath.ToSlash(name)
	if cfg.TestFilePrefix != "" && strings.HasPrefix(clean, cfg.TestFilePrefix) {
		return true
	}
	for _, suffix := range cfg.TestFileSuffixes {
		if strings.HasSuffix(clean, suffix) {
			return true
		}
	}
	return false
}

func parseSuites(name string, data []byte) ([]TestSuite, error) {
	var suite TestSuite
	if err := yaml.Unmarshal(data, &suite); err == nil {
		if len(suite.Cases) > 0 {
			if suite.Name == "" {
				suite.Name = name
			}
			return []TestSuite{suite}, nil
		}
	}

	var cases []TestCase
	if err := yaml.Unmarshal(data, &cases); err == nil {
		if len(cases) > 0 {
			return []TestSuite{{Name: name, Cases: cases}}, nil
		}
	}

	var single TestCase
	if err := yaml.Unmarshal(data, &single); err == nil {
		if single.Name != "" {
			return []TestSuite{{Name: name, Cases: []TestCase{single}}}, nil
		}
	}

	return nil, fmt.Errorf("no test cases found")
}
