package floci

import (
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
)

// The test-case schema and its legacy-fixture lowering live in the shared
// internal/fixture package so the goTester consumes the exact same canonical
// shape (see that package's doc comment for the schema contract). The aliases
// below keep the floci-local vocabulary (TestCase, Assertion) that the
// setup/checker registries and the external dataset pipeline were written
// against.

// TestCase is a single Floci integration test; see fixture.TestCase.
type TestCase = fixture.TestCase

// Assertion is a single declarative setup action or side-effect check; see
// fixture.Assertion.
type Assertion = fixture.Assertion

// LoadTestCasesFromDir reads every *.json file in dir as a TestCase. Files are
// loaded in lexical order for deterministic runs.
func LoadTestCasesFromDir(dir string) ([]TestCase, error) {
	return fixture.LoadFromDir(dir)
}

// TestCasesFromPackage builds Floci test cases from the test files bundled in a
// DeploymentPackage (the `test/` entries of the uploaded zip). This lets a
// single upload carry both the function and its integration tests, without a
// separate on-disk test_cases_dir. Legacy black-box fixtures are lowered into
// the canonical shape automatically.
func TestCasesFromPackage(pkg *domain.DeploymentPackage) ([]TestCase, error) {
	return fixture.FromPackage(pkg)
}

// parsePackageTestCase interprets a single bundled test file, preferring the
// rich canonical format and lowering the legacy black-box fixture format.
func parsePackageTestCase(name string, raw []byte) (TestCase, error) {
	return fixture.Parse(name, raw)
}
