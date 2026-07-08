package pipeline

import (
	"fmt"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// Converter defines a single conversion step that can be applied to a
// ConversionRequest within a Runner.
type Converter interface {
	Apply(*Runner, *domain.ConversionRequest) error
}

// ConverterFactory creates a Converter from a set of configuration options.
type ConverterFactory func(map[string]interface{}) Converter

var converterFactories = map[string]ConverterFactory{}

// RegisterConverterFactory registers a converter factory by name.
func RegisterConverterFactory(name string, factory ConverterFactory) {
	if name == "" || factory == nil {
		return
	}
	converterFactories[name] = factory
}

// MakeConverter looks up a converter factory by key and builds a Converter using args.
func MakeConverter(key string, args map[string]interface{}) (Converter, error) {
	if key == "" {
		return nil, nil
	}
	if factory, ok := converterFactories[key]; ok {
		return factory(args), nil
	}
	return nil, fmt.Errorf("no converter found for key: %s", key)
}

// NoOpConverter is a converter that performs no work.
type NoOpConverter struct{}

// Apply is a no-op implementation.
func (NoOpConverter) Apply(*Runner, *domain.ConversionRequest) error { return nil }

// NewNoopConverter returns a no-op converter.
func NewNoopConverter(args map[string]interface{}) Converter {
	return &NoOpConverter{}
}

// CanCompileConverter validates required package inputs before build/test steps.
type CanCompileConverter struct{}

// Apply validates that both source and working packages are present and aligned.
func (CanCompileConverter) Apply(run *Runner, req *domain.ConversionRequest) error {
	if req.SourcePackage == nil {
		return fmt.Errorf("no Package source defined")
	}

	if req.WorkingPackage == nil {
		return fmt.Errorf("no Package working directory defined")
	}

	if req.SourcePackage.RootFile == "" {
		return fmt.Errorf("no source root file defined")
	}

	if req.WorkingPackage.RootFile == "" {
		return fmt.Errorf("no working root file defined")
	}

	if len(req.SourcePackage.TestFiles) != len(req.WorkingPackage.TestFiles) {
		return fmt.Errorf("number of test files and test files don't match")
	}

	return nil
}

// NewCompilePrecheckConverter returns a converter that validates compile preconditions.
func NewCompilePrecheckConverter(args map[string]interface{}) Converter {
	return &CanCompileConverter{}
}

func init() {
	RegisterConverterFactory("noop", NewNoopConverter)
	RegisterConverterFactory("canCompile", NewCompilePrecheckConverter)
}
