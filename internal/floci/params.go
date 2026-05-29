package floci

import (
	"fmt"
	"strings"
)

func requireString(params map[string]any, key string) (string, error) {
	value := getParamString(params, key)
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("missing required param: %s", key)
	}
	return value, nil
}

func getParamString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case string:
			return v
		case fmt.Stringer:
			return v.String()
		}
	}
	return ""
}

func getParamBool(params map[string]any, key string) bool {
	if params == nil {
		return false
	}
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			trimmed := strings.ToLower(strings.TrimSpace(v))
			return trimmed == "true" || trimmed == "1" || trimmed == "yes"
		}
	}
	return false
}

func getParamMap(params map[string]any, key string) map[string]any {
	if params == nil {
		return nil
	}
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case map[string]any:
			return v
		}
	}
	return nil
}
