// Package pipeline_test is an external test package so it can import the
// converter-providing packages (translator, builder) for their factory
// init() registrations without creating an import cycle with pipeline.
package pipeline_test

import (
	"strings"
	"testing"

	_ "github.com/carl-egge/ise-predictive-refaas/internal/builder"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	_ "github.com/carl-egge/ise-predictive-refaas/internal/translator"
)

// TestDefaultPipelineStillCompiles makes sure the embedded default pipeline
// keeps compiling under the stricter unresolved-reference checks.
func TestDefaultPipelineStillCompiles(t *testing.T) {
	p, err := pipeline.PipelineReader(strings.NewReader(pipeline.DefaultPipelineYAML))
	if err != nil {
		t.Fatalf("embedded default.yaml no longer compiles: %v", err)
	}
	if p == nil || p.FirstTask == nil {
		t.Fatal("expected a compiled pipeline with a root task")
	}
}
