package inputhandler

import (
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

func validPackage() *domain.DeploymentPackage {
	return &domain.DeploymentPackage{
		RootFile: "def handler(event, context):\n    return {}",
		TestFiles: map[string]string{
			"test/t1.json": `{"payload":{},"expectedOutput":{"statusCode":200}}`,
		},
	}
}

func TestValidateAcceptsUsablePackage(t *testing.T) {
	if err := Validate(validPackage(), ValidateOptions{}); err != nil {
		t.Fatalf("a valid package must pass: %v", err)
	}
	// legacy fixture dialect must pass too - fixture.Parse lowers it
	legacy := validPackage()
	legacy.TestFiles = map[string]string{"test/t1.json": `{"input":"{}","output":"{}"}`}
	if err := Validate(legacy, ValidateOptions{}); err != nil {
		t.Fatalf("legacy fixtures must pass: %v", err)
	}
}

// TestValidateRejectsUnusablePackages guards [C6]: a package that cannot
// produce a meaningful result must be rejected before any LLM call. Zero
// fixtures matters most - the test stage would otherwise pass vacuously and
// report a success that validated nothing.
func TestValidateRejectsUnusablePackages(t *testing.T) {
	cases := []struct {
		name string
		pkg  *domain.DeploymentPackage
		want string
	}{
		{"nil package", nil, "could not be read"},
		{"empty archive", &domain.DeploymentPackage{}, "no source file"},
		{
			"no fixtures",
			&domain.DeploymentPackage{RootFile: "def handler(): pass"},
			"no test fixtures",
		},
		{
			"unparseable fixture",
			&domain.DeploymentPackage{
				RootFile:  "def handler(): pass",
				TestFiles: map[string]string{"test/t1.json": "{not json"},
			},
			"invalid test fixture test/t1.json",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.pkg, ValidateOptions{})
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), c.want)
			}
		})
	}
}

// TestValidateReportsEveryProblem verifies a bad artifact takes one upload to
// diagnose, not several.
func TestValidateReportsEveryProblem(t *testing.T) {
	err := Validate(&domain.DeploymentPackage{}, ValidateOptions{RequireMeta: true})
	if err == nil {
		t.Fatal("expected a validation error")
	}
	var ve *ValidationError
	if !asValidationError(err, &ve) {
		t.Fatalf("expected a *ValidationError, got %T", err)
	}
	if len(ve.Problems) < 3 {
		t.Errorf("expected source/fixture/meta problems reported together, got %v", ve.Problems)
	}
}

// TestValidateMetaRequirement guards the maintainer's decision: meta.json is
// required for benchmark runs (an unattributable result is worth nothing
// after hours of LLM spend) but must stay optional otherwise, so the bundled
// examples and ad-hoc uploads keep working.
func TestValidateMetaRequirement(t *testing.T) {
	pkg := validPackage()

	if err := Validate(pkg, ValidateOptions{}); err != nil {
		t.Fatalf("meta.json must be optional by default: %v", err)
	}

	err := Validate(pkg, ValidateOptions{RequireMeta: true})
	if err == nil {
		t.Fatal("benchmark mode must reject a package without meta.json")
	}
	if !strings.Contains(err.Error(), domain.MetaFileName) {
		t.Errorf("error should name the missing file, got: %v", err)
	}

	pkg.Meta = &domain.FunctionMeta{Bucket: "A"}
	if err := Validate(pkg, ValidateOptions{RequireMeta: true}); err != nil {
		t.Fatalf("a package with meta.json must pass in benchmark mode: %v", err)
	}
}

// TestValidateBlocksFlociFixturesWhenDisabled guards the [C10] admission
// rule: a function whose fixtures assert AWS state cannot be validated with
// the Floci route off, so it must be refused before the pipeline spends the
// full LLM budget on a result that could only be vacuous or an
// infrastructure failure.
func TestValidateBlocksFlociFixturesWhenDisabled(t *testing.T) {
	pkg := validPackage()
	pkg.TestFiles["test/t2.json"] = `{"name":"store-message","payload":{"bucket":"audit"},
		"setup":[{"type":"s3.bucket","bucket":"audit"}],
		"sideEffects":[{"type":"s3.objectExists","bucket":"audit","key":"m1.json"}]}`

	err := Validate(pkg, ValidateOptions{FlociEnabled: false})
	if err == nil {
		t.Fatal("expected side-effect fixtures to be rejected while floci is disabled")
	}
	if !strings.Contains(err.Error(), "FLOCI_ENABLED") {
		t.Errorf("error should say how to enable the route, got: %v", err)
	}
	if !strings.Contains(err.Error(), "store-message") {
		t.Errorf("error should name the offending fixture, got: %v", err)
	}

	if err := Validate(pkg, ValidateOptions{FlociEnabled: true}); err != nil {
		t.Fatalf("the same package must be accepted when floci is enabled: %v", err)
	}
}

// TestValidateAllowsPureFixturesWithFlociOff: the common case must not be
// affected - including the canonical schema's empty setup/sideEffects blocks.
func TestValidateAllowsPureFixturesWithFlociOff(t *testing.T) {
	pkg := validPackage()
	pkg.TestFiles["test/t2.json"] = `{"payload":{},"expectedOutput":{},"setup":[],"sideEffects":[]}`

	if err := Validate(pkg, ValidateOptions{FlociEnabled: false}); err != nil {
		t.Fatalf("empty setup/sideEffects must not require the floci route: %v", err)
	}
}

func TestBenchmarkValidateOptionsFromEnv(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "1"} {
		t.Setenv("REQUIRE_META", v)
		if !BenchmarkValidateOptions().RequireMeta {
			t.Errorf("REQUIRE_META=%q should enable the check", v)
		}
	}
	for _, v := range []string{"", "false", "0", "no"} {
		t.Setenv("REQUIRE_META", v)
		if BenchmarkValidateOptions().RequireMeta {
			t.Errorf("REQUIRE_META=%q should leave the check off", v)
		}
	}
}

// asValidationError is a local errors.As shim keeping the test readable.
func asValidationError(err error, target **ValidationError) bool {
	ve, ok := err.(*ValidationError)
	if ok {
		*target = ve
	}
	return ok
}
