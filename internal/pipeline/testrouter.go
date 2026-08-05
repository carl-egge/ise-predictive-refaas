package pipeline

import (
	"fmt"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
	log "github.com/sirupsen/logrus"
)

// Converter names this router dispatches to. They are resolved through the
// global factory registry rather than by importing internal/builder and
// internal/floci, which keeps the Floci integration an optional,
// blank-imported dependency of the binary.
const (
	goTesterName    = "goTester"
	flociTesterName = "flociTester"
)

// TestRouterConverter picks the validation route for one job ([C10]).
//
// A function's fixtures decide it: cases that only carry payload/expectedOutput
// are fully validated black-box by goTester, while cases declaring setup or
// sideEffects assert AWS state that only a real deployment into the Floci
// emulator can check. Running the wrong one is not a neutral choice - goTester
// on an AWS function exercises the SDK with no emulator behind it, which both
// fails for infrastructure reasons that look like translation defects and
// risks touching real AWS ([C11]).
//
// The router never validates nothing: if a job needs Floci and Floci is
// unavailable it fails loudly, because the alternative - a silent pass - would
// report a success that validated nothing at all.
type TestRouterConverter struct {
	goTester    Converter
	flociTester Converter
}

func init() {
	RegisterConverterFactory("testRouter", NewTestRouterConverter)
}

// NewTestRouterConverter builds the router, resolving both testers by name.
// args are passed through to each, so task_args meant for either (e.g.
// test_timeout, function_name) keep working.
func NewTestRouterConverter(args map[string]interface{}) Converter {
	router := &TestRouterConverter{}

	if conv, err := MakeConverter(goTesterName, args); err != nil {
		log.Warnf("testRouter: %s unavailable: %v", goTesterName, err)
	} else {
		router.goTester = conv
	}
	// Absent whenever internal/floci is not linked into the binary; that is a
	// supported build, so this is not an error until a job actually needs it.
	if conv, err := MakeConverter(flociTesterName, args); err != nil {
		log.Debugf("testRouter: %s unavailable (floci not linked in): %v", flociTesterName, err)
	} else {
		router.flociTester = conv
	}

	return router
}

// Apply classifies the job's fixtures and runs exactly one tester.
func (r *TestRouterConverter) Apply(runner *Runner, req *domain.ConversionRequest) error {
	cases, err := r.testCases(req)
	if err != nil {
		return err
	}

	if !fixture.RequiresFloci(cases) {
		if r.goTester == nil {
			return fmt.Errorf("testRouter: %s is not registered", goTesterName)
		}
		log.Debugf("testRouter: no fixture declares side effects, validating with %s", goTesterName)
		return r.goTester.Apply(runner, req)
	}

	// From here the job can only be validated by the Floci route. Every exit
	// is an error rather than a skip: silently passing would report a
	// success for a function whose AWS behaviour was never checked.
	if !runner.FlociEnabled() {
		return fmt.Errorf(
			"testRouter: these fixtures declare setup/sideEffects and can only be validated by the Floci route, but the Floci backend is disabled (set FLOCI_ENABLED=true or floci.enabled in the pipeline config)")
	}
	if r.flociTester == nil {
		return fmt.Errorf("testRouter: these fixtures require the Floci route, but %s is not registered in this binary", flociTesterName)
	}
	log.Debugf("testRouter: fixtures declare side effects, validating with %s", flociTesterName)
	return r.flociTester.Apply(runner, req)
}

// testCases parses the fixtures to classify the job. The source package is
// preferred because it is never mutated by a pipeline stage; the working
// package is the fallback for requests assembled without one.
func (r *TestRouterConverter) testCases(req *domain.ConversionRequest) ([]fixture.TestCase, error) {
	pkg := req.SourcePackage
	if pkg == nil || len(pkg.TestFiles) == 0 {
		pkg = req.WorkingPackage
	}
	if pkg == nil {
		return nil, fmt.Errorf("testRouter: no package with test fixtures to validate")
	}
	cases, err := fixture.FromPackage(pkg)
	if err != nil {
		return nil, fmt.Errorf("testRouter: cannot classify fixtures: %w", err)
	}
	return cases, nil
}
