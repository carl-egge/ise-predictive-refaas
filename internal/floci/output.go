package floci

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// SubsetJSONValidator validates JSON output using subset matching.
type SubsetJSONValidator struct{}

// Validate checks that the actual payload matches the expected subset.
func (SubsetJSONValidator) Validate(actual []byte, expected any) error {
	if expected == nil {
		return nil
	}

	actualValue, err := decodeJSON(actual)
	if err != nil {
		return fmt.Errorf("failed to decode lambda output: %w", err)
	}

	normalized := normalizeLambdaBody(actualValue, expected)
	if !subsetMatch(expected, normalized) {
		return fmt.Errorf("lambda output mismatch")
	}
	return nil
}

func decodeJSON(payload []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var out any
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeLambdaBody(actual any, expected any) any {
	actualMap, ok := actual.(map[string]any)
	if !ok {
		if expMap, okExp := expected.(map[string]any); okExp {
			if strVal, okStr := actual.(string); okStr && looksLikeJSON(strVal) {
				var parsed any
				if err := json.Unmarshal([]byte(strVal), &parsed); err == nil {
					return parsed
				}
			}
			_ = expMap
		}
		return actual
	}

	expectedMap, ok := expected.(map[string]any)
	if !ok {
		return actual
	}

	if expBody, ok := expectedMap["body"]; ok {
		if bodyStr, okBody := actualMap["body"].(string); okBody && looksLikeJSON(bodyStr) {
			var parsed any
			if err := json.Unmarshal([]byte(bodyStr), &parsed); err == nil {
				actualMap["body"] = parsed
				_ = expBody
			}
		}
	}
	return actualMap
}

func looksLikeJSON(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func subsetMatch(expected, actual any) bool {
	switch exp := expected.(type) {
	case map[string]any:
		actMap, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, expVal := range exp {
			actVal, ok := actMap[key]
			if !ok {
				return false
			}
			if !subsetMatch(expVal, actVal) {
				return false
			}
		}
		return true
	case []any:
		actSlice, ok := actual.([]any)
		if !ok {
			return false
		}
		if len(exp) != len(actSlice) {
			return false
		}
		for i := range exp {
			if !subsetMatch(exp[i], actSlice[i]) {
				return false
			}
		}
		return true
	default:
		return primitiveMatch(expected, actual)
	}
}

func primitiveMatch(expected, actual any) bool {
	if expNum, ok := expected.(json.Number); ok {
		return numberMatch(expNum, actual)
	}
	if actNum, ok := actual.(json.Number); ok {
		return numberMatch(actNum, expected)
	}
	return reflect.DeepEqual(expected, actual)
}

func numberMatch(num json.Number, other any) bool {
	floatVal, err := num.Float64()
	if err != nil {
		return false
	}
	switch v := other.(type) {
	case json.Number:
		otherFloat, err := v.Float64()
		if err != nil {
			return false
		}
		return floatVal == otherFloat
	case float64:
		return floatVal == v
	case float32:
		return floatVal == float64(v)
	case int:
		return floatVal == float64(v)
	case int32:
		return floatVal == float64(v)
	case int64:
		return floatVal == float64(v)
	case uint:
		return floatVal == float64(v)
	case uint32:
		return floatVal == float64(v)
	case uint64:
		return floatVal == float64(v)
	default:
		return false
	}
}
