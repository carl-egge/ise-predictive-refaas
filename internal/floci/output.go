package floci

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// matchOutput validates a Lambda response against an expected value in a
// black-box, formatting-tolerant way. Both sides are parsed as JSON and
// compared as a subset: every field present in expected must appear in actual
// with an equal value, while extra fields in actual are ignored. This mirrors
// the goTester's lenient comparison but is structural rather than
// string-similarity based.
//
// If either side is not valid JSON, it falls back to a trimmed substring
// comparison so plain-string handlers still validate.
func matchOutput(expected, actual []byte) error {
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

	if ok, path := jsonSubset(exp, act); !ok {
		return fmt.Errorf("output mismatch at %s: expected %s, got %s",
			path, compact(expected), compact(actual))
	}
	return nil
}

// jsonSubset reports whether want is a subset of got. On mismatch it returns a
// dotted path to the first divergence for a readable error message.
func jsonSubset(want, got interface{}) (bool, string) {
	switch w := want.(type) {
	case map[string]interface{}:
		g, ok := got.(map[string]interface{})
		if !ok {
			return false, "$"
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				return false, k
			}
			if ok, sub := jsonSubset(wv, gv); !ok {
				return false, joinPath(k, sub)
			}
		}
		return true, ""
	case []interface{}:
		g, ok := got.([]interface{})
		if !ok || len(g) != len(w) {
			return false, "[]"
		}
		for i := range w {
			if ok, sub := jsonSubset(w[i], g[i]); !ok {
				return false, joinPath(fmt.Sprintf("[%d]", i), sub)
			}
		}
		return true, ""
	default:
		if !scalarsEqual(want, got) {
			return false, "."
		}
		return true, ""
	}
}

func joinPath(head, tail string) string {
	if tail == "" || tail == "." {
		return head
	}
	return head + "." + tail
}

func compact(b []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return string(b)
	}
	return buf.String()
}
