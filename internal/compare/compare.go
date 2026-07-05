// Package compare provides the single, shared definition of "the output
// matches the expectation" used by both validation routes (the black-box
// goTester and the Floci integration tester). Keeping one implementation
// makes experimental results comparable between the two routes and gives
// both the same flexibility spectrum: strict equivalence, tolerant scalar
// matching, and type-shape-only comparison for non-deterministic outputs.
package compare

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Mode selects how strictly JSON values are compared.
type Mode int

const (
	// Strict requires matching scalar types and values (subset semantics on
	// objects: extra fields in the actual value are ignored). A stringified
	// number ("200" vs 200) is a mismatch - catching exactly that class of
	// translation bug is the point of this mode.
	Strict Mode = iota
	// Tolerant compares scalars leniently: numbers directly, everything
	// else by string form ("3" matches 3). The historical Floci behavior,
	// useful for assertions against loosely-typed mock-service state.
	Tolerant
	// ShapeOnly requires only matching structure and value *types*; scalar
	// values are ignored and array lengths may differ (each actual element
	// is checked against the shape of the first expected element). This is
	// the comparison for non-deterministic outputs (live APIs, timestamps,
	// generated ids), where value equivalence is impossible by design.
	ShapeOnly
)

// JSONSubset reports whether want is structurally contained in got under
// mode. On mismatch it returns a dotted path to the first divergence for a
// readable error message. String values that themselves contain JSON (the
// harness's JSON-encoded response body is the prime example) are decoded and
// compared structurally, so formatting and key order never matter.
func JSONSubset(want, got interface{}, mode Mode) (bool, string) {
	want = normalize(want)
	got = normalize(got)

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
			if ok, sub := JSONSubset(wv, gv, mode); !ok {
				return false, joinPath(k, sub)
			}
		}
		return true, ""
	case []interface{}:
		g, ok := got.([]interface{})
		if !ok {
			return false, "$"
		}
		if mode == ShapeOnly {
			// non-deterministic lists may vary in length; require each
			// actual element to match the shape of the first expected one
			if len(w) == 0 {
				return true, ""
			}
			for i := range g {
				if ok, sub := JSONSubset(w[0], g[i], mode); !ok {
					return false, joinPath(fmt.Sprintf("[%d]", i), sub)
				}
			}
			return true, ""
		}
		if len(g) != len(w) {
			return false, "[]"
		}
		for i := range w {
			if ok, sub := JSONSubset(w[i], g[i], mode); !ok {
				return false, joinPath(fmt.Sprintf("[%d]", i), sub)
			}
		}
		return true, ""
	default:
		if !scalarsMatch(want, got, mode) {
			return false, "."
		}
		return true, ""
	}
}

// scalarsMatch compares two JSON leaves under the given mode.
func scalarsMatch(want, got interface{}, mode Mode) bool {
	switch got.(type) {
	case map[string]interface{}, []interface{}:
		// a container can never satisfy a scalar expectation
		return false
	}
	switch mode {
	case ShapeOnly:
		return jsonType(want) == jsonType(got)
	case Tolerant:
		if wf, ok := want.(float64); ok {
			if gf, ok := got.(float64); ok {
				return wf == gf
			}
		}
		return fmt.Sprintf("%v", want) == fmt.Sprintf("%v", got)
	default: // Strict
		if want == nil || got == nil {
			return want == nil && got == nil
		}
		if jsonType(want) != jsonType(got) {
			return false
		}
		return want == got
	}
}

// jsonType names a decoded JSON value's type for shape comparison and
// mismatch messages.
func jsonType(v interface{}) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	}
	return fmt.Sprintf("%T", v)
}

// normalize decodes string values that themselves carry a JSON container so
// they compare structurally instead of byte-for-byte; anything else is
// returned unchanged.
func normalize(v interface{}) interface{} {
	s, ok := v.(string)
	if !ok {
		return v
	}
	trimmed := strings.TrimSpace(s)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return v
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return v
	}
	return decoded
}

func joinPath(head, tail string) string {
	if tail == "" || tail == "." {
		return head
	}
	return head + "." + tail
}
