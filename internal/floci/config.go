package floci

import (
	"context"
	"sync"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	log "github.com/sirupsen/logrus"
)

const (
	// DefaultEndpoint is Floci's single AWS endpoint.
	DefaultEndpoint = "http://localhost:4566"
	// DefaultRegion is the region used for all Floci clients.
	DefaultRegion = "us-east-1"
)

// config is the active Floci configuration, populated by Start when the
// pipeline is built/reconfigured with floci.enabled=true. The flociTester stage
// reads it to decide whether to run and where to reach the emulator. Guarded by
// a mutex because Start (on /reconfigure) and a running stage may race.
var (
	configMu  sync.RWMutex
	activeCfg pipeline.FlociConfig
)

func init() {
	// Linking in this package enables the feature: the pipeline can now start
	// the Floci backend on demand and resolve the "flociTester" task.
	pipeline.RegisterFlociStarter(Start)
	pipeline.RegisterConverterFactory("flociTester", NewFlociTester)
}

// Start records the resolved configuration and kicks off a background
// reachability check of the emulator. It is the backend "runner" referenced by
// the task brief: it only runs when floci.enabled is set.
//
// Recording the config is synchronous (it is local, no network I/O) so that
// callers observe the new endpoint/region immediately. The reachability ping
// itself is a network round-trip against Floci and is deliberately run in the
// background: Start is invoked from pipeline.ConverterOptions.startFloci while
// the service holds its global request-handling lock (see
// ConverterService.reconfigure), so blocking here on an unreachable emulator
// (up to several seconds) would stall every other in-flight request. Ping
// failures are only diagnostic - the flociTester stage does its own reachable
// check (and returns a hard error) at execution time.
func Start(cfg pipeline.FlociConfig) error {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}

	configMu.Lock()
	activeCfg = cfg
	configMu.Unlock()

	log.Infof("floci: backend enabled (endpoint=%s region=%s)", cfg.Endpoint, cfg.Region)

	go checkReachable(cfg)
	return nil
}

// checkReachable pings the configured Floci endpoint, logs the result, and
// warms the Lambda-visible endpoint cache. It runs asynchronously from Start so
// neither an unreachable emulator nor the (multi-second) endpoint probe blocks
// pipeline construction/reconfiguration.
func checkReachable(cfg pipeline.FlociConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clients, err := NewClients(ctx, cfg.Endpoint, cfg.Region)
	if err != nil {
		log.Warnf("floci: could not build AWS clients for %s: %v", cfg.Endpoint, err)
		return
	}
	if err := clients.Ping(ctx); err != nil {
		log.Warnf("floci backend not reachable yet: %v", err)
		return
	}
	log.Infof("floci: emulator reachable at %s", cfg.Endpoint)

	// Warm the Lambda-visible endpoint while the service is idle. It is
	// resolved by building and invoking a probe Lambda (see probe.go), which
	// takes seconds; doing it here means the first conversion finds it cached
	// instead of paying for it. Still background work, so a slow or failing
	// probe delays nothing - the stage resolves it itself if this has not
	// finished.
	pctx, pcancel := context.WithTimeout(context.Background(), probeTimeout)
	defer pcancel()
	resolveLambdaEndpoint(pctx, clients, cfg.LambdaEndpoint)
}

// activeConfig returns a snapshot of the current Floci configuration.
func activeConfig() pipeline.FlociConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	return activeCfg
}
