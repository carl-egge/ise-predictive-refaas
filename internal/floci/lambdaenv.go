package floci

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

// The Lambda-visible AWS endpoint ([C11], revised).
//
// A translated function runs inside a Lambda *container*, not in this process.
// The endpoint this process uses (default http://localhost:4566) is a host-side
// address that resolves, inside that container, to the container itself - so it
// cannot simply be forwarded. The original code drew the opposite conclusion
// and set no AWS_ENDPOINT_URL at all, trusting the emulator to inject one.
//
// Run 20260831-190900 showed that trust is misplaced: 35 of 52 Floci execution
// errors were UnrecognizedClientException (21), InvalidAccessKeyId (8) and
// PermanentRedirect (6) - the signature of the SDK resolving *real* AWS and
// presenting the dummy credentials. The translated code was not at fault; every
// generated main.go inspected guards its override with
// `if ep := os.Getenv("AWS_ENDPOINT_URL"); ep != ""`, which was simply never
// taken. On the goTester route, where TestExecutionEnv does force the endpoint,
// AWS functions passed 11/13 against 5/28 here.
//
// So the endpoint must be injected - just not this process's copy of it.
// Floci documents the container -> emulator path per deployment shape
// (docs/floci.md "Lambda on native Linux Docker"): the docker bridge gateway on
// native Linux, the Docker VM on Docker Desktop, and container IPs on a shared
// network when Floci itself runs in Docker. Rather than guess which one applies,
// resolveLambdaEndpoint asks a probe Lambda deployed into the emulator (see
// probe.go), which can read its own default gateway and test each candidate
// from where it matters. The static candidates below are the fallback for when
// the probe cannot run.

// lambdaEndpointDisabled are the lambda_endpoint values that restore the old
// behaviour of injecting nothing and trusting the emulator. Spelled out because
// "" already means auto-detect.
var lambdaEndpointDisabled = map[string]bool{"off": true, "none": true, "-": true}

// endpointCache memoises the resolved Lambda-visible endpoint per configured
// endpoint. Resolution deploys and invokes a probe Lambda, which is far too
// expensive to repeat for all 95 functions of a benchmark run, and the answer
// is a property of the deployment topology, not of the function under test.
var (
	endpointCacheMu sync.Mutex
	endpointCache   = map[string]string{}
)

// lambdaEnv is the environment the deployed function runs with.
//
// endpoint is the Lambda-visible endpoint from resolveLambdaEndpoint; empty
// means inject nothing (lambda_endpoint=off).
//
// The credentials are dummy on purpose: they are what the emulator expects, and
// they mean a translated function that somehow escaped the endpoint override
// still cannot authenticate against a real AWS account. That containment held
// in the failing run - which is why it produced InvalidAccessKeyId errors
// rather than writes to somebody's real bucket - but it is the last line of
// defence, not the mechanism.
func lambdaEnv(region, endpoint string) map[string]string {
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":         "test",
		"AWS_SECRET_ACCESS_KEY":     "test",
		"AWS_SESSION_TOKEN":         "test",
		"AWS_REGION":                region,
		"AWS_DEFAULT_REGION":        region,
		"AWS_EC2_METADATA_DISABLED": "true",
		// Read by boto3 and the JS SDK. The Go SDK v2 has no path-style
		// environment variable at all - it needs s3.Options.UsePathStyle in
		// code, the way NewClients sets it for our own client - so this is
		// documentation of intent for the emulator's benefit, not a control
		// that reaches a translated Go function. The translate prompts are
		// what must ask for path-style S3.
		"AWS_S3_FORCE_PATH_STYLE": "true",
	}
	if endpoint != "" {
		env["AWS_ENDPOINT_URL"] = endpoint
	}
	return env
}

// resolveLambdaEndpoint determines the endpoint a deployed Lambda must use to
// reach the emulator, and caches it for the process.
//
// Order: an explicit lambda_endpoint wins (and "off" disables injection
// entirely); otherwise the probe Lambda answers from inside the emulator; if
// the probe cannot run, the first static candidate is used. The fallback is
// deliberately a *wrong-but-local* address rather than nothing: an unreachable
// endpoint fails fast against localhost, whereas an unset one sends the SDK to
// real AWS - the same reasoning Runner.FlociEndpoint documents for the
// goTester route.
func resolveLambdaEndpoint(ctx context.Context, c *Clients, configured string) string {
	if v := strings.TrimSpace(configured); v != "" {
		if lambdaEndpointDisabled[strings.ToLower(v)] {
			log.Warnf("floci: lambda_endpoint=%q - deploying without AWS_ENDPOINT_URL; "+
				"a translated function will reach real AWS unless the emulator injects one", v)
			return ""
		}
		return v
	}

	endpointCacheMu.Lock()
	cached, ok := endpointCache[c.Endpoint]
	endpointCacheMu.Unlock()
	if ok {
		return cached
	}

	candidates := staticCandidates(c.Endpoint)
	resolved, err := probeLambdaEndpoint(ctx, c, candidates)
	if err != nil {
		resolved = candidates[0]
		log.Warnf("floci: endpoint probe failed (%v); falling back to %q. "+
			"Set floci.lambda_endpoint / FLOCI_LAMBDA_ENDPOINT if that is wrong",
			err, resolved)
	}

	log.Infof("floci: lambda-visible AWS endpoint resolved to %s (this process uses %s)",
		resolved, c.Endpoint)

	// A probe cut short by the caller's own cancellation says nothing about the
	// topology, so it must not be cached: /stop on the first job of a benchmark
	// run would otherwise pin the fallback for all 95. Every other outcome is
	// cached, including a genuine probe failure - repeating a build-and-deploy
	// per function to relearn the same answer is not worth it.
	if err != nil && ctx.Err() != nil {
		return resolved
	}
	endpointCacheMu.Lock()
	endpointCache[c.Endpoint] = resolved
	endpointCacheMu.Unlock()
	return resolved
}

// staticCandidates lists the endpoints a Lambda container might reach the
// emulator on, best first, for the deployment shapes floci.md describes.
//
// For a loopback endpoint the container needs a different host: the docker
// bridge gateway on native Linux (the probe discovers the real one from its own
// routing table; 172.17.0.1 is only the default-bridge fallback for when the
// probe cannot run) or host.docker.internal under Docker Desktop. A
// non-loopback endpoint - "http://floci:4566" from docker-compose, say - is
// already a name both sides share, so it is offered unchanged and first.
func staticCandidates(endpoint string) []string {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return []string{endpoint}
	}

	host := u.Hostname()
	if !isLoopbackHost(host) {
		// Reachable under the same name from both sides; keep the alternatives
		// as a fallback in case the emulator is only published on the host.
		return dedupe([]string{endpoint,
			swapHost(u, "host.docker.internal"),
			swapHost(u, defaultBridgeGateway)})
	}
	return dedupe([]string{
		swapHost(u, defaultBridgeGateway),
		swapHost(u, "host.docker.internal"),
		endpoint,
	})
}

// defaultBridgeGateway is docker0's conventional address. The probe reports the
// container's actual default gateway, which is authoritative; this constant is
// only the offline guess.
const defaultBridgeGateway = "172.17.0.1"

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "", "localhost", "0.0.0.0", "::", "[::]":
		return true
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// swapHost rewrites u's host, keeping scheme, port and path.
func swapHost(u *url.URL, host string) string {
	c := *u
	if port := u.Port(); port != "" {
		c.Host = net.JoinHostPort(host, port)
	} else {
		c.Host = host
	}
	return strings.TrimRight(c.String(), "/")
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return []string{DefaultEndpoint}
	}
	return out
}

// endpointPort is the port the probe should try against its own gateway when
// none of the supplied candidates is reachable.
func endpointPort(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "4566"
	}
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// describeUnreachable renders the probe's per-candidate errors for a log line.
func describeUnreachable(errs map[string]string) string {
	if len(errs) == 0 {
		return "no candidates tried"
	}
	parts := make([]string, 0, len(errs))
	for cand, msg := range errs {
		parts = append(parts, fmt.Sprintf("%s (%s)", cand, msg))
	}
	return strings.Join(parts, "; ")
}
