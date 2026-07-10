package floci

import (
	"context"
	"fmt"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	log "github.com/sirupsen/logrus"
)

// FlociTester is the pipeline stage that deploys the translated working package
// as a Lambda into Floci and validates it against declarative test cases. It is
// registered as the "flociTester" converter.
//
// It only does work when the Floci backend has been enabled
// (floci.enabled=true). Otherwise it is a no-op, so adding the stage to a
// pipeline never breaks runs where the feature is turned off.
type FlociTester struct {
	// functionName is the Lambda name to deploy under. Multiple conversions
	// reuse the name (update-in-place); fine for a single-worker service.
	functionName string
	// testCasesDir, when set, loads rich (side-effect-aware) test cases from a
	// directory of *.json files. When empty, the stage derives basic cases from
	// the package's own black-box fixtures.
	testCasesDir string
}

// NewFlociTester builds the stage from merged task params. Recognised keys:
//
//	function_name   string  Lambda name (default "translated-fn")
//	test_cases_dir  string  directory of *.json Floci test cases (optional)
func NewFlociTester(args map[string]interface{}) pipeline.Converter {
	t := &FlociTester{functionName: "translated-fn"}
	if v, ok := args["function_name"].(string); ok && v != "" {
		t.functionName = v
	}
	if v, ok := args["test_cases_dir"].(string); ok {
		t.testCasesDir = v
	}
	return t
}

// Apply runs the integration test: package -> deploy -> for each case: setup ->
// invoke -> validate output -> check side effects.
func (t *FlociTester) Apply(runner *pipeline.Runner, req *domain.ConversionRequest) error {
	cfg := activeConfig()
	if !cfg.Enabled {
		log.Debugf("floci: stage skipped (floci.enabled=false)")
		return nil
	}
	if req.WorkingPackage == nil {
		return fmt.Errorf("floci: no working package to test")
	}

	// runner embeds context.Context, so cancellation (e.g. /stop) propagates
	// into the AWS calls and the build subprocess.
	ctx := context.Context(runner)

	clients, err := NewClients(ctx, cfg.Endpoint, cfg.Region)
	if err != nil {
		return err
	}
	if err := clients.Ping(ctx); err != nil {
		return err // clear "emulator unavailable" error
	}

	cases, err := t.loadCases(req.WorkingPackage)
	if err != nil {
		return err
	}

	start := time.Now()
	zipBytes, err := packageLambda(ctx, req.WorkingPackage)
	if err != nil {
		return err
	}
	if err := deployLambda(ctx, clients, t.functionName, zipBytes); err != nil {
		return err
	}
	log.Debugf("floci: deployed %q in %s, running %d test case(s)", t.functionName, time.Since(start), len(cases))

	failed := 0
	for _, tc := range cases {
		if err := t.runCase(ctx, clients, tc); err != nil {
			failed++
			log.Errorf("floci: test case %q failed: %v", tc.Name, err)
			req.AddError(fmt.Errorf("floci case %q: %w", tc.Name, err))
			continue
		}
		log.Debugf("floci: test case %q passed", tc.Name)
	}

	if failed > 0 {
		return domain.NewTestingError(fmt.Errorf("%d/%d floci test cases failed", failed, len(cases)), failed)
	}
	log.Infof("floci: all %d test case(s) passed for %q", len(cases), t.functionName)
	return nil
}

// loadCases prefers a configured rich test-case directory and falls back to the
// package's own black-box fixtures.
func (t *FlociTester) loadCases(pkg *domain.DeploymentPackage) ([]TestCase, error) {
	if t.testCasesDir != "" {
		return LoadTestCasesFromDir(t.testCasesDir)
	}
	return TestCasesFromPackage(pkg)
}

// runCase executes one test case end to end.
func (t *FlociTester) runCase(ctx context.Context, clients *Clients, tc TestCase) error {
	for _, action := range tc.Setup {
		if err := runSetup(ctx, clients, action); err != nil {
			return err
		}
	}

	payload := tc.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	resp, err := invoke(ctx, clients, t.functionName, payload)
	if err != nil {
		return err
	}

	if err := matchOutput(tc.ExpectedOutput, resp, tc.CompareMode()); err != nil {
		return err
	}

	for _, effect := range tc.SideEffects {
		if err := runChecker(ctx, clients, effect); err != nil {
			return err
		}
	}
	return nil
}
