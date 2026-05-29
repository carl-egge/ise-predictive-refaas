package floci

import (
	"fmt"
	"strings"
)

// Config holds configuration for Floci-backed integration tests.
type Config struct {
	Enabled              bool
	Endpoint             string
	Region               string
	AccountID            string
	FunctionName         string
	LambdaRoleARN        string
	LambdaTimeoutSeconds int32
	LambdaMemoryMB       int32
	GoOS                 string
	GoArch               string
	UsePathStyle         bool
	TestFilePrefix       string
	TestFileSuffixes     []string
}

// DefaultConfig returns a Config with opinionated defaults for local Floci use.
func DefaultConfig() Config {
	return Config{
		Enabled:              false,
		Endpoint:             "http://localhost:4566",
		Region:               "us-east-1",
		AccountID:            "000000000000",
		FunctionName:         "refaas-translated",
		LambdaRoleARN:        "arn:aws:iam::000000000000:role/lambda-role",
		LambdaTimeoutSeconds: 10,
		LambdaMemoryMB:       128,
		GoOS:                 "linux",
		GoArch:               "amd64",
		UsePathStyle:         true,
		TestFilePrefix:       "test/floci/",
		TestFileSuffixes:     []string{".floci.json", ".floci.yaml", ".floci.yml"},
	}
}

// ConfigFromArgs merges args from the pipeline config with defaults.
func ConfigFromArgs(args map[string]interface{}) (Config, error) {
	cfg := DefaultConfig()
	if args == nil {
		return cfg, nil
	}

	cfg.Enabled = getBool(args, "floci_enabled", cfg.Enabled)
	cfg.Endpoint = getString(args, "floci_endpoint", cfg.Endpoint)
	cfg.Region = getString(args, "floci_region", cfg.Region)
	cfg.AccountID = getString(args, "floci_account_id", cfg.AccountID)
	cfg.FunctionName = getString(args, "floci_function_name", cfg.FunctionName)
	cfg.LambdaRoleARN = getString(args, "floci_lambda_role_arn", cfg.LambdaRoleARN)
	cfg.LambdaTimeoutSeconds = int32(getInt(args, "floci_lambda_timeout_seconds", int(cfg.LambdaTimeoutSeconds)))
	cfg.LambdaMemoryMB = int32(getInt(args, "floci_lambda_memory_mb", int(cfg.LambdaMemoryMB)))
	cfg.GoOS = getString(args, "floci_goos", cfg.GoOS)
	cfg.GoArch = getString(args, "floci_goarch", cfg.GoArch)
	cfg.UsePathStyle = getBool(args, "floci_use_path_style", cfg.UsePathStyle)
	cfg.TestFilePrefix = getString(args, "floci_test_prefix", cfg.TestFilePrefix)
	cfg.TestFileSuffixes = getStringSlice(args, "floci_test_suffixes", cfg.TestFileSuffixes)

	if cfg.Enabled && strings.TrimSpace(cfg.Endpoint) == "" {
		return cfg, fmt.Errorf("floci_endpoint is required when floci_enabled is true")
	}
	return cfg, nil
}

func getString(args map[string]interface{}, key, def string) string {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return v
			}
		case fmt.Stringer:
			return v.String()
		}
	}
	return def
}

func getBool(args map[string]interface{}, key string, def bool) bool {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			trimmed := strings.ToLower(strings.TrimSpace(v))
			if trimmed == "true" || trimmed == "1" || trimmed == "yes" {
				return true
			}
			if trimmed == "false" || trimmed == "0" || trimmed == "no" {
				return false
			}
		}
	}
	return def
}

func getInt(args map[string]interface{}, key string, def int) int {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case int32:
			return int(v)
		case int64:
			return int(v)
		case float32:
			return int(v)
		case float64:
			return int(v)
		case string:
			var out int
			_, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &out)
			if err == nil {
				return out
			}
		}
	}
	return def
}

func getStringSlice(args map[string]interface{}, key string, def []string) []string {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case []string:
			return v
		case []interface{}:
			out := make([]string, 0, len(v))
			for _, item := range v {
				switch vv := item.(type) {
				case string:
					out = append(out, vv)
				case fmt.Stringer:
					out = append(out, vv.String())
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return def
}
