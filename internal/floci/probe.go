package floci

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	log "github.com/sirupsen/logrus"
)

// probeFunctionName is the Lambda the endpoint probe is deployed as. It is
// deliberately distinct from the translated function's name so a probe never
// overwrites (or is overwritten by) the code under test.
const probeFunctionName = "floci-endpoint-probe"

// probeTimeout bounds the whole probe - build, deploy and invoke. A probe that
// hangs must not delay the conversion it is supposed to make work, and the
// static candidates are a usable fallback.
const probeTimeout = 3 * time.Minute

// probeSource is a minimal Lambda that answers the one question this process
// cannot: which address reaches the emulator *from inside a Lambda container*.
//
// It reports three things, in the order they are trusted:
//
//  1. AWS_ENDPOINT_URL as the emulator injected it, if it did (the assumption
//     the previous implementation rested on - now measured rather than assumed);
//  2. its own default gateway, which on native Linux Docker is the bridge
//     address Floci documents as the container -> host path (docs/floci.md,
//     "Lambda on native Linux Docker"), read from the routing table so the real
//     bridge is used rather than an assumed 172.17.0.1;
//  3. the caller's static candidates.
//
// It uses only the standard library plus aws-lambda-go: an AWS SDK client here
// would make the probe depend on the very configuration it is measuring.
const probeSource = `package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type probeRequest struct {
	Candidates []string ` + "`json:\"candidates\"`" + `
	Port       string   ` + "`json:\"port\"`" + `
}

type probeResponse struct {
	Endpoint string            ` + "`json:\"endpoint\"`" + `
	Injected string            ` + "`json:\"injected\"`" + `
	Gateway  string            ` + "`json:\"gateway\"`" + `
	Tried    []string          ` + "`json:\"tried\"`" + `
	Errors   map[string]string ` + "`json:\"errors\"`" + `
}

func handle(ctx context.Context, raw json.RawMessage) (probeResponse, error) {
	var req probeRequest
	_ = json.Unmarshal(raw, &req)

	res := probeResponse{Errors: map[string]string{}}
	res.Injected = strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL"))
	res.Gateway = defaultGateway()

	port := req.Port
	if port == "" {
		port = "4566"
	}

	seen := map[string]bool{}
	var cands []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		cands = append(cands, s)
	}
	add(res.Injected)
	if res.Gateway != "" {
		add("http://" + net.JoinHostPort(res.Gateway, port))
	}
	for _, c := range req.Candidates {
		add(c)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	for _, c := range cands {
		res.Tried = append(res.Tried, c)
		if err := reach(ctx, client, c); err != nil {
			res.Errors[c] = err.Error()
			continue
		}
		res.Endpoint = c
		return res, nil
	}
	return res, nil
}

// reach counts any HTTP response as success: the emulator answers "/" with
// whatever it likes, and all we need to know is that the TCP+HTTP path exists.
func reach(ctx context.Context, client *http.Client, endpoint string) error {
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// defaultGateway reads the container's default route out of /proc/net/route.
// The Gateway column is a little-endian hex word, so 010011AC is 172.17.0.1.
func defaultGateway() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		v, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			continue
		}
		return fmt.Sprintf("%d.%d.%d.%d", byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	}
	return ""
}
`

// probeResult mirrors probeSource's probeResponse on this side of the wire.
type probeResult struct {
	Endpoint string            `json:"endpoint"`
	Injected string            `json:"injected"`
	Gateway  string            `json:"gateway"`
	Tried    []string          `json:"tried"`
	Errors   map[string]string `json:"errors"`
}

// probeLambdaEndpoint builds, deploys and invokes the probe, returning the
// first endpoint that answered from inside the emulator.
//
// It is deployed with lambdaEnv(region, "") - no AWS_ENDPOINT_URL - precisely so
// the probe can report whether the emulator injects one of its own.
func probeLambdaEndpoint(ctx context.Context, c *Clients, candidates []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	zipBytes, err := packageLambda(ctx, &domain.DeploymentPackage{RootFile: probeSource})
	if err != nil {
		return "", fmt.Errorf("building probe: %w", err)
	}
	if err := deployLambda(ctx, c, probeFunctionName, zipBytes, lambdaEnv(c.Region, "")); err != nil {
		return "", fmt.Errorf("deploying probe: %w", err)
	}

	payload, err := json.Marshal(map[string]any{
		"candidates": candidates,
		"port":       endpointPort(c.Endpoint),
	})
	if err != nil {
		return "", fmt.Errorf("encoding probe payload: %w", err)
	}

	raw, err := invoke(ctx, c, probeFunctionName, payload)
	if err != nil {
		// A timeout here is itself diagnostic on native Linux Docker: Floci
		// documents UFW's default INPUT DROP silently killing container -> host
		// packets, which surfaces as Function.TimedOut for every invocation.
		return "", fmt.Errorf("invoking probe: %w (if this is Function.TimedOut on native Linux Docker, "+
			"see docs/floci.md \"Lambda on native Linux Docker\": sudo ufw allow in on docker0)", err)
	}

	var res probeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("decoding probe response %q: %w", truncate(string(raw), 200), err)
	}
	if res.Injected != "" {
		log.Debugf("floci: emulator injects AWS_ENDPOINT_URL=%s into Lambda containers", res.Injected)
	}
	if res.Gateway != "" {
		log.Debugf("floci: probe container default gateway is %s", res.Gateway)
	}
	if res.Endpoint == "" {
		return "", fmt.Errorf("no candidate reachable from inside the emulator: %s",
			describeUnreachable(res.Errors))
	}
	return res.Endpoint, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
