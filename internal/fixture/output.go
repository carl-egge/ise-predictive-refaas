package fixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/compare"
)

// MatchOutput validates a function response against an expected value using
// the shared comparator (internal/compare) - the single definition of
// "equivalent" used by both validation routes (goTester and the Floci
// stage), so results are comparable between them. mode selects the
// flexibility level: tolerant subset matching by default, strict scalar
// typing, or type-shape-only for non-deterministic outputs (see
// TestCase.OutputMode).
//
// If either side is not valid JSON, it falls back to a trimmed substring
// comparison so plain-string handlers still validate.
func MatchOutput(expected, actual []byte, mode compare.Mode) error {
	if len(expected) == 0 {
		return nil // no expectation declared
	}

	var exp, act interface{}
	expErr := json.Unmarshal(expected, &exp)
	actErr := json.Unmarshal(actual, &act)

	if expErr != nil || actErr != nil {
		e := strings.TrimSpace(string(expected))
		a := strings.TrimSpace(string(actual))
		if strings.Contains(a, e) {
			return nil
		}
		return fmt.Errorf("output %q does not contain expected %q", a, e)
	}

	if ok, path := compare.JSONSubset(exp, act, mode); !ok {
		return fmt.Errorf("output mismatch at %s: expected %s, got %s",
			path, compact(expected), compact(actual))
	}
	return nil
}

func compact(b []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return string(b)
	}
	return buf.String()
}
