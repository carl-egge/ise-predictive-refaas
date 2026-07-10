package floci

import (
	"github.com/carl-egge/ise-predictive-refaas/internal/compare"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
)

// matchOutput validates a Lambda response against an expected value. The
// implementation lives in the shared fixture package (fixture.MatchOutput) so
// the goTester judges outputs by the exact same definition of "equivalent";
// the vectors in output_test.go pin its semantics for the external dataset
// pipeline, which is verified against them.
func matchOutput(expected, actual []byte, mode compare.Mode) error {
	return fixture.MatchOutput(expected, actual, mode)
}
