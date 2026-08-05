package builder

import (
	"strings"
	"testing"
)

// envMap resolves an environment slice the way exec.Cmd does: last occurrence
// of a key wins.
func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// TestTestExecutionEnvStripsHostCredentials is the core [C11] guarantee: a
// developer's or CI runner's ambient AWS credentials must never reach a
// translated function, or a translated PutObject becomes a real write.
func TestTestExecutionEnvStripsHostCredentials(t *testing.T) {
	host := []string{
		"PATH=/usr/bin",
		"AWS_ACCESS_KEY_ID=AKIAREALCREDENTIAL",
		"AWS_SECRET_ACCESS_KEY=realsecret",
		"AWS_SESSION_TOKEN=realtoken",
		"AWS_PROFILE=production",
		"AWS_REGION=eu-central-1",
	}

	env := envMap(TestExecutionEnv(host, nil, nil, "http://localhost:4566", "us-east-1"))

	if env["AWS_ACCESS_KEY_ID"] != harnessAccessKeyID || env["AWS_SECRET_ACCESS_KEY"] != harnessSecretAccessKey {
		t.Errorf("host credentials leaked into the test environment: %v", env)
	}
	if _, ok := env["AWS_PROFILE"]; ok {
		t.Error("AWS_PROFILE must not survive: a profile can name real credentials or a role to assume")
	}
	if env["PATH"] != "/usr/bin" {
		t.Error("non-AWS host variables must be preserved")
	}
}

// TestTestExecutionEnvForcesEmulatorEndpoint: the endpoint and credentials are
// the containment, so neither the package .env nor a fixture may override them.
func TestTestExecutionEnvForcesEmulatorEndpoint(t *testing.T) {
	pkgEnv := []string{"AWS_ENDPOINT_URL=https://s3.amazonaws.com", "TABLE_NAME=users"}
	caseEnv := []string{"AWS_ACCESS_KEY_ID=AKIASOMETHINGREAL"}

	env := envMap(TestExecutionEnv([]string{"PATH=/usr/bin"}, pkgEnv, caseEnv, "http://localhost:4566", "us-east-1"))

	if env["AWS_ENDPOINT_URL"] != "http://localhost:4566" {
		t.Errorf("AWS_ENDPOINT_URL = %q, want the harness endpoint to win", env["AWS_ENDPOINT_URL"])
	}
	if env["AWS_ACCESS_KEY_ID"] != harnessAccessKeyID {
		t.Errorf("credentials must not be overridable, got %q", env["AWS_ACCESS_KEY_ID"])
	}
	if env["AWS_EC2_METADATA_DISABLED"] != "true" {
		t.Error("instance metadata lookup must be disabled, or the SDK can still find credentials")
	}
	if env["TABLE_NAME"] != "users" {
		t.Error("non-AWS package variables must still reach the function")
	}
}

// TestTestExecutionEnvRegionIsOverridable: region is behavioural rather than
// leakage-critical - the dataset has a function that derives its region from
// the invoked ARN - so a fixture may pin it.
func TestTestExecutionEnvRegionIsOverridable(t *testing.T) {
	env := envMap(TestExecutionEnv(nil, nil, []string{"AWS_REGION=eu-west-1"}, "http://localhost:4566", "us-east-1"))

	if env["AWS_REGION"] != "eu-west-1" {
		t.Errorf("AWS_REGION = %q, want the fixture's value to win", env["AWS_REGION"])
	}
	if env["AWS_DEFAULT_REGION"] != "us-east-1" {
		t.Errorf("AWS_DEFAULT_REGION = %q, want the harness default", env["AWS_DEFAULT_REGION"])
	}
}

// TestTestExecutionEnvDoesNotAliasInputs guards against the classic append
// bug: the prefix is shared across test cases, so a per-case env must not
// write into it.
func TestTestExecutionEnvDoesNotAliasInputs(t *testing.T) {
	host := make([]string, 0, 32)
	host = append(host, "PATH=/usr/bin")

	first := TestExecutionEnv(host, nil, []string{"CASE=one"}, "http://e", "r")
	second := TestExecutionEnv(host, nil, []string{"CASE=two"}, "http://e", "r")

	if envMap(first)["CASE"] != "one" || envMap(second)["CASE"] != "two" {
		t.Errorf("environments alias each other: %v / %v", envMap(first)["CASE"], envMap(second)["CASE"])
	}
}

// TestTestExecutionEnvFailsClosedWithoutConfig: an empty endpoint would make
// the SDK resolve the real AWS endpoint, so the guard must supply its own
// fallback rather than trusting the caller to be configured.
func TestTestExecutionEnvFailsClosedWithoutConfig(t *testing.T) {
	env := envMap(TestExecutionEnv(nil, nil, nil, "", ""))

	if env["AWS_ENDPOINT_URL"] != fallbackEndpoint {
		t.Errorf("AWS_ENDPOINT_URL = %q, want the fallback endpoint - empty would resolve to real AWS", env["AWS_ENDPOINT_URL"])
	}
	if env["AWS_REGION"] != fallbackRegion {
		t.Errorf("AWS_REGION = %q, want the fallback region", env["AWS_REGION"])
	}
}

func TestStripAWSEnv(t *testing.T) {
	got := StripAWSEnv([]string{"HOME=/root", "AWS_REGION=x", "AWSOME=keepme"})
	if len(got) != 2 {
		t.Fatalf("StripAWSEnv = %v, want HOME and AWSOME kept", got)
	}
	if got[1] != "AWSOME=keepme" {
		t.Errorf("only the AWS_ prefix should be stripped, got %v", got)
	}
}
