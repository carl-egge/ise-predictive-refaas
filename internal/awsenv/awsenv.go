package awsenv

import (
	"os"
	"sort"
	"strings"
)

var flociEndpointKeys = []string{
	"AWS_ENDPOINT_URL_S3",
	"AWS_ENDPOINT_URL_DYNAMODB",
	"AWS_ENDPOINT_URL_LAMBDA",
	"AWS_ENDPOINT_URL_SQS",
	"AWS_ENDPOINT_URL_SNS",
	"AWS_ENDPOINT_URL_STS",
	"AWS_ENDPOINT_URL_KMS",
	"AWS_ENDPOINT_URL_SECRETS_MANAGER",
}

// MergeEnv combines base env values with overrides, returning a map.
func MergeEnv(base []string, overrides []string) map[string]string {
	out := make(map[string]string, len(base)+len(overrides))
	insertEnv(out, base)
	insertEnv(out, overrides)
	return out
}

// FlattenEnv returns a sorted list of KEY=VALUE entries.
func FlattenEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

// NormalizeEndpoint rewrites the Floci service hostname to localhost when
// running outside Docker.
func NormalizeEndpoint(endpoint string) string {
	if endpoint == "" {
		return endpoint
	}
	if runningInDocker() {
		return endpoint
	}
	return strings.Replace(endpoint, "://floci:", "://localhost:", 1)
}

// Augment adds Floci-aware defaults when an endpoint is configured.
func Augment(env map[string]string, endpoint string) map[string]string {
	out := copyEnv(env)
	resolved := endpoint
	if resolved == "" {
		resolved = out["AWS_ENDPOINT_URL"]
	}
	if resolved == "" {
		return out
	}
	out["AWS_ENDPOINT_URL"] = resolved
	for _, key := range flociEndpointKeys {
		out[key] = resolved
	}
	setDefault(out, "AWS_S3_FORCE_PATH_STYLE", "true")
	setDefault(out, "AWS_EC2_METADATA_DISABLED", "true")
	setDefault(out, "AWS_ACCESS_KEY_ID", "test")
	setDefault(out, "AWS_SECRET_ACCESS_KEY", "test")
	setDefault(out, "AWS_REGION", "us-east-1")
	setDefault(out, "AWS_DEFAULT_REGION", out["AWS_REGION"])
	return out
}

func insertEnv(dest map[string]string, items []string) {
	for _, entry := range items {
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		dest[parts[0]] = parts[1]
	}
}

func setDefault(env map[string]string, key, value string) {
	if env[key] == "" {
		env[key] = value
	}
}

func copyEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

func runningInDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "docker") || strings.Contains(string(data), "containerd")
}
