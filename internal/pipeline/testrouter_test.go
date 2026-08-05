package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// recordingConverter notes whether it ran, standing in for a tester.
type recordingConverter struct{ ran bool }

func (c *recordingConverter) Apply(*Runner, *domain.ConversionRequest) error {
	c.ran = true
	return nil
}

const (
	blackBoxFixture   = `{"payload":{"a":1},"expectedOutput":{"statusCode":200}}`
	legacyFixture     = `{"input":"{\"a\":1}","output":"{\"statusCode\":200}"}`
	emptyBlocksFixure = `{"payload":{},"expectedOutput":{},"setup":[],"sideEffects":[]}`
	sideEffectFixture = `{"payload":{"bucket":"audit"},"expectedOutput":{"statusCode":200},
		"setup":[{"type":"s3.bucket","bucket":"audit"}],
		"sideEffects":[{"type":"s3.objectExists","bucket":"audit","key":"m1.json"}]}`
)

func routerWith(goTester, flociTester Converter) *TestRouterConverter {
	return &TestRouterConverter{goTester: goTester, flociTester: flociTester}
}

func requestWithFixtures(fixtures map[string]string) *domain.ConversionRequest {
	return &domain.ConversionRequest{
		SourcePackage: &domain.DeploymentPackage{
			RootFile:  "def handler(event, context):\n    return {}",
			TestFiles: fixtures,
		},
	}
}

// TestRouterUsesGoTesterWithoutSideEffects covers the two "standard go
// testing" rows of the [C10] matrix: a function whose fixtures assert only a
// response is validated black-box, whether or not Floci happens to be on.
func TestRouterUsesGoTesterWithoutSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fixture      string
		flociEnabled bool
	}{
		{"canonical schema, floci off", blackBoxFixture, false},
		{"canonical schema, floci on", blackBoxFixture, true},
		{"legacy input/output schema", legacyFixture, false},
		{"canonical schema with empty setup/sideEffects", emptyBlocksFixure, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			goTester, flociTester := &recordingConverter{}, &recordingConverter{}
			router := routerWith(goTester, flociTester)
			runner := NewRunner(context.Background(), nil, nil)
			runner.floci = FlociConfig{Enabled: tc.flociEnabled}

			if err := router.Apply(runner, requestWithFixtures(map[string]string{"test/t1.json": tc.fixture})); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if !goTester.ran {
				t.Error("expected the black-box tester to run")
			}
			if flociTester.ran {
				t.Error("the floci route must not run for a function without side effects")
			}
		})
	}
}

// TestRouterUsesFlociWhenFixturesRequireIt covers the "floci enabled and
// required" row: one side-effect case anywhere in the set routes the job.
func TestRouterUsesFlociWhenFixturesRequireIt(t *testing.T) {
	goTester, flociTester := &recordingConverter{}, &recordingConverter{}
	router := routerWith(goTester, flociTester)
	runner := NewRunner(context.Background(), nil, nil)
	runner.floci = FlociConfig{Enabled: true}

	req := requestWithFixtures(map[string]string{
		"test/t1.json": blackBoxFixture,
		"test/t2.json": sideEffectFixture,
	})
	if err := router.Apply(runner, req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !flociTester.ran {
		t.Error("expected the floci route to run")
	}
	if goTester.ran {
		t.Error("goTester must not also run: it would exercise the AWS SDK with no emulator behind it")
	}
}

// TestRouterBlocksWhenFlociRequiredButDisabled covers the fourth row. The
// job must fail loudly - a skip would report a success for a function whose
// AWS behaviour was never checked.
func TestRouterBlocksWhenFlociRequiredButDisabled(t *testing.T) {
	goTester, flociTester := &recordingConverter{}, &recordingConverter{}
	router := routerWith(goTester, flociTester)
	runner := NewRunner(context.Background(), nil, nil)
	runner.floci = FlociConfig{Enabled: false}

	err := router.Apply(runner, requestWithFixtures(map[string]string{"test/t1.json": sideEffectFixture}))
	if err == nil {
		t.Fatal("expected an error when the floci route is required but disabled")
	}
	if !strings.Contains(err.Error(), "FLOCI_ENABLED") {
		t.Errorf("error should say how to enable it, got: %v", err)
	}
	if goTester.ran || flociTester.ran {
		t.Error("no tester should have run")
	}
}

// TestRouterErrorsWhenFlociNotLinkedIn: a binary built without internal/floci
// must not silently pass a job that needs it.
func TestRouterErrorsWhenFlociNotLinkedIn(t *testing.T) {
	router := routerWith(&recordingConverter{}, nil)
	runner := NewRunner(context.Background(), nil, nil)
	runner.floci = FlociConfig{Enabled: true}

	err := router.Apply(runner, requestWithFixtures(map[string]string{"test/t1.json": sideEffectFixture}))
	if err == nil || !strings.Contains(err.Error(), "flociTester") {
		t.Fatalf("expected a clear 'not registered' error, got: %v", err)
	}
}

// TestRouterFallsBackToWorkingPackage: requests assembled without a source
// package (or with its fixtures dropped) must still classify.
func TestRouterFallsBackToWorkingPackage(t *testing.T) {
	goTester := &recordingConverter{}
	router := routerWith(goTester, &recordingConverter{})
	runner := NewRunner(context.Background(), nil, nil)

	req := &domain.ConversionRequest{
		WorkingPackage: &domain.DeploymentPackage{
			TestFiles: map[string]string{"test/t1.json": blackBoxFixture},
		},
	}
	if err := router.Apply(runner, req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !goTester.ran {
		t.Error("expected classification to fall back to the working package")
	}
}

func TestRouterErrorsWithoutFixtures(t *testing.T) {
	router := routerWith(&recordingConverter{}, &recordingConverter{})
	runner := NewRunner(context.Background(), nil, nil)

	if err := router.Apply(runner, &domain.ConversionRequest{}); err == nil {
		t.Fatal("expected an error when there is nothing to classify")
	}
}

// TestRouterFactoryResolvesRegisteredTesters verifies the router finds the
// testers through the registry (goTester is registered by internal/builder,
// which the pipeline package does not import).
func TestRouterFactoryResolvesRegisteredTesters(t *testing.T) {
	RegisterConverterFactory(goTesterName, func(map[string]interface{}) Converter {
		return &recordingConverter{}
	})
	t.Cleanup(func() { delete(converterFactories, goTesterName) })

	router, ok := NewTestRouterConverter(map[string]interface{}{}).(*TestRouterConverter)
	if !ok {
		t.Fatal("factory did not return a *TestRouterConverter")
	}
	if router.goTester == nil {
		t.Error("expected the registered goTester to be resolved by name")
	}
}
