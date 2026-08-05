package builder

import (
	"fmt"
	"slices"
	"strings"
)

// Dummy credentials handed to translated functions. They are the conventional
// emulator values: the local AWS emulator accepts anything, while real AWS
// rejects them - which is exactly the property we want, since a translated
// function that ignores the endpoint override then cannot perform a side
// effect against a real account.
const (
	harnessAccessKeyID     = "test"
	harnessSecretAccessKey = "test"
	harnessSessionToken    = "test"
)

// awsEnvPrefix identifies the variables that steer AWS SDK credential and
// endpoint resolution.
const awsEnvPrefix = "AWS_"

// Fallbacks used when the caller supplies no endpoint or region. They matter:
// an empty AWS_ENDPOINT_URL is worse than a wrong one, because the SDK then
// resolves the *real* AWS endpoint for the service. The guard must therefore
// fail closed on its own, without depending on a fully configured Runner.
const (
	fallbackEndpoint = "http://localhost:4566"
	fallbackRegion   = "us-east-1"
)

// TestExecutionEnv assembles the environment a translated function runs under
// ([C11]).
//
// Scraped serverless functions call AWS, and the pipeline executes them for
// real. Three things therefore have to be true, in this order of precedence:
//
//  1. No AWS_* variable of the *host* reaches the function. A developer's or
//     CI runner's ambient credentials are the one thing that could turn a
//     translated PutObject into a real write, so they are stripped wholesale
//     rather than selectively overridden.
//  2. Region and addressing style are defaults, so a fixture may legitimately
//     pin a different region (the dataset has a function that derives its
//     region from the invoked ARN).
//  3. Endpoint, credentials and instance-metadata lookup are forced last and
//     cannot be overridden by the package's .env or a fixture: they are what
//     keeps the traffic inside the emulator.
//
// Note what this does and does not guarantee: a translated function that
// ignores AWS_ENDPOINT_URL entirely may still attempt an outbound connection
// to a real AWS endpoint, but with dummy credentials it cannot authenticate,
// so no data is read or written. Preventing the connection attempt itself
// would need network isolation, which is out of scope here.
func TestExecutionEnv(hostEnv, pkgEnv, caseEnv []string, endpoint, region string) []string {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = fallbackEndpoint
	}
	if strings.TrimSpace(region) == "" {
		region = fallbackRegion
	}

	defaults := []string{
		fmt.Sprintf("AWS_REGION=%s", region),
		fmt.Sprintf("AWS_DEFAULT_REGION=%s", region),
		// path-style addressing: a local emulator has no per-bucket DNS
		"AWS_S3_FORCE_PATH_STYLE=true",
	}

	forced := []string{
		fmt.Sprintf("AWS_ENDPOINT_URL=%s", endpoint),
		fmt.Sprintf("AWS_ACCESS_KEY_ID=%s", harnessAccessKeyID),
		fmt.Sprintf("AWS_SECRET_ACCESS_KEY=%s", harnessSecretAccessKey),
		fmt.Sprintf("AWS_SESSION_TOKEN=%s", harnessSessionToken),
		// without this the SDK can fall back to the instance metadata service
		// for credentials when the static ones are rejected
		"AWS_EC2_METADATA_DISABLED=true",
	}

	// exec.Cmd resolves duplicate keys to the last occurrence, so ordering is
	// the precedence mechanism here. Concat allocates a fresh slice, which
	// matters because this is called per test case with shared inputs.
	return slices.Concat(StripAWSEnv(hostEnv), defaults, pkgEnv, caseEnv, forced)
}

// StripAWSEnv returns environ without any AWS_* variable, so ambient
// credentials cannot reach a translated function.
func StripAWSEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if strings.HasPrefix(kv, awsEnvPrefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
