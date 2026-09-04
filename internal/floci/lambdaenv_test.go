package floci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLambdaEnvInjectsEndpoint pins the fix for run 20260831-190900: a deployed
// Lambda must be told where the emulator is. Every generated main.go inspected
// from that run guards its override with
// `if ep := os.Getenv("AWS_ENDPOINT_URL"); ep != ""`, so an absent variable is
// not a neutral default - it routes the function to real AWS.
func TestLambdaEnvInjectsEndpoint(t *testing.T) {
	env := lambdaEnv("eu-central-1", "http://172.17.0.1:4566")

	if got := env["AWS_ENDPOINT_URL"]; got != "http://172.17.0.1:4566" {
		t.Errorf("AWS_ENDPOINT_URL = %q, want the lambda-visible endpoint", got)
	}
	if got := env["AWS_REGION"]; got != "eu-central-1" {
		t.Errorf("AWS_REGION = %q, want eu-central-1", got)
	}
	// The dummy credentials are the containment layer: they are why the failing
	// run produced InvalidAccessKeyId instead of writes to a real account.
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		if env[k] != "test" {
			t.Errorf("%s = %q, want the dummy credential", k, env[k])
		}
	}
	if env["AWS_EC2_METADATA_DISABLED"] != "true" {
		t.Error("AWS_EC2_METADATA_DISABLED must stay set: the metadata endpoint is another route to real credentials")
	}
}

// TestLambdaEnvOmitsEmptyEndpoint guards the fail-closed detail [C11] already
// records for the goTester route: an *empty* AWS_ENDPOINT_URL is worse than an
// absent one, because the SDK treats it as "resolve normally" after we have
// advertised that we set it.
func TestLambdaEnvOmitsEmptyEndpoint(t *testing.T) {
	env := lambdaEnv("us-east-1", "")
	if _, ok := env["AWS_ENDPOINT_URL"]; ok {
		t.Errorf("AWS_ENDPOINT_URL present with empty endpoint: %q", env["AWS_ENDPOINT_URL"])
	}
}

func TestStaticCandidates(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantHead string
		wantAll  []string
	}{
		{
			// The failing run's shape: service on the host, Floci native, so
			// "localhost" inside a Lambda container is the container itself.
			// floci.md names the docker bridge gateway as the path out.
			name:     "loopback prefers the bridge gateway",
			endpoint: "http://localhost:4566",
			wantHead: "http://172.17.0.1:4566",
			wantAll: []string{
				"http://172.17.0.1:4566",
				"http://host.docker.internal:4566",
				"http://localhost:4566",
			},
		},
		{
			name:     "loopback by IP is treated the same",
			endpoint: "http://127.0.0.1:4566",
			wantHead: "http://172.17.0.1:4566",
		},
		{
			// docker-compose.yml's FLOCI_ENDPOINT: a service name both the
			// pipeline container and the Lambda container resolve identically.
			name:     "shared hostname is offered unchanged",
			endpoint: "http://floci:4566",
			wantHead: "http://floci:4566",
		},
		{
			name:     "non-default port is preserved",
			endpoint: "http://localhost:9999",
			wantHead: "http://172.17.0.1:9999",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := staticCandidates(tc.endpoint)
			if len(got) == 0 {
				t.Fatal("no candidates returned")
			}
			if got[0] != tc.wantHead {
				t.Errorf("first candidate = %q, want %q (all: %v)", got[0], tc.wantHead, got)
			}
			if tc.wantAll != nil {
				if len(got) != len(tc.wantAll) {
					t.Fatalf("candidates = %v, want %v", got, tc.wantAll)
				}
				for i := range got {
					if got[i] != tc.wantAll[i] {
						t.Errorf("candidate %d = %q, want %q", i, got[i], tc.wantAll[i])
					}
				}
			}
			// Whatever the shape, the configured endpoint must never be the
			// only answer offered for a loopback host.
			seen := map[string]bool{}
			for _, c := range got {
				if seen[c] {
					t.Errorf("duplicate candidate %q in %v", c, got)
				}
				seen[c] = true
			}
		})
	}
}

func TestEndpointPort(t *testing.T) {
	for endpoint, want := range map[string]string{
		"http://localhost:4566": "4566",
		"http://floci":          "80",
		"https://floci":         "443",
	} {
		if got := endpointPort(endpoint); got != want {
			t.Errorf("endpointPort(%q) = %q, want %q", endpoint, got, want)
		}
	}
}

// TestResolveLambdaEndpointExplicit covers the two paths that must not touch
// the network: an operator override, and the "off" escape hatch that restores
// the previous behaviour.
func TestResolveLambdaEndpointExplicit(t *testing.T) {
	c := &Clients{Endpoint: "http://localhost:4566", Region: "us-east-1"}

	if got := resolveLambdaEndpoint(context.Background(), c, "http://emulator:4566"); got != "http://emulator:4566" {
		t.Errorf("explicit lambda_endpoint = %q, want it to win", got)
	}
	for _, off := range []string{"off", "OFF", "none", "-"} {
		if got := resolveLambdaEndpoint(context.Background(), c, off); got != "" {
			t.Errorf("lambda_endpoint=%q returned %q, want no injection", off, got)
		}
	}
}

func TestResolveLambdaEndpointUsesCache(t *testing.T) {
	c := &Clients{Endpoint: "http://cached-endpoint:4566", Region: "us-east-1"}

	endpointCacheMu.Lock()
	endpointCache[c.Endpoint] = "http://resolved:4566"
	endpointCacheMu.Unlock()
	t.Cleanup(func() {
		endpointCacheMu.Lock()
		delete(endpointCache, c.Endpoint)
		endpointCacheMu.Unlock()
	})

	// A cache hit must not attempt to build/deploy a probe, which is what makes
	// this safe to call once per function across a 95-function benchmark run.
	if got := resolveLambdaEndpoint(context.Background(), c, ""); got != "http://resolved:4566" {
		t.Errorf("cached endpoint = %q, want http://resolved:4566", got)
	}
}

// TestResolveLambdaEndpointDoesNotCacheCancelled guards a benchmark-run hazard:
// a /stop on the first function must not pin the fallback endpoint for the
// remaining 94. A cancelled probe reports nothing about the topology.
func TestResolveLambdaEndpointDoesNotCacheCancelled(t *testing.T) {
	c := &Clients{Endpoint: "http://cancelled-endpoint:4566", Region: "us-east-1"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := resolveLambdaEndpoint(ctx, c, "")
	if got == "" {
		t.Error("resolve returned no endpoint; an unset AWS_ENDPOINT_URL resolves to real AWS")
	}

	endpointCacheMu.Lock()
	_, cached := endpointCache[c.Endpoint]
	delete(endpointCache, c.Endpoint)
	endpointCacheMu.Unlock()
	if cached {
		t.Error("a cancelled probe was cached; later jobs would inherit the fallback")
	}
}

// TestProbeSourceCompiles keeps the probe honest: it lives in a string literal,
// so nothing else in the build would catch a syntax or type error in it, and
// the failure would only surface as a warning during a benchmark run. It
// imports only the standard library, so this needs no network.
func TestProbeSourceCompiles(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available")
	}

	dir := t.TempDir()
	// probeSource has no main(); packageLambda supplies lambdaMain, which
	// depends on aws-lambda-go. A local stub keeps this test offline while
	// still type-checking handle's signature against a lambda.Start-shaped use.
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write("go.mod", "module probecheck\n\ngo 1.21\n")
	write("main.go", probeSource)
	write("stub_main.go", `package main

import (
	"context"
	"encoding/json"
)

// mirrors lambda.Start's constraint on the handler we hand it.
var _ func(context.Context, json.RawMessage) (probeResponse, error) = handle

func main() {}
`)

	cmd := exec.Command(goBin, "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("probe source does not compile: %v\n%s", err, out)
	}
}
