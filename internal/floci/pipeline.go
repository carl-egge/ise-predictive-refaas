package floci

import (
	"context"
	"fmt"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	"github.com/sirupsen/logrus"
)

// FlociTester runs Floci-backed integration tests as an optional pipeline stage.
type FlociTester struct {
	cfg Config
}

func init() {
	pipeline.RegisterConverterFactory("flociTester", NewFlociTester)
}

// NewFlociTester builds a Floci tester from pipeline args.
func NewFlociTester(args map[string]interface{}) pipeline.Converter {
	cfg, err := ConfigFromArgs(args)
	if err != nil {
		logrus.WithError(err).Warn("invalid floci config")
	}
	return &FlociTester{cfg: cfg}
}

// Apply executes Floci integration tests when enabled.
func (f *FlociTester) Apply(runner *pipeline.Runner, request *domain.ConversionRequest) error {
	if !f.cfg.Enabled {
		return nil
	}
	if request.WorkingPackage == nil {
		return fmt.Errorf("missing working package for floci tests")
	}
	ctx := runner.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return RunIntegrationTests(ctx, f.cfg, request.WorkingPackage)
}
