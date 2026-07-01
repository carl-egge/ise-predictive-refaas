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

// Start records the resolved configuration and verifies the emulator is
// reachable. It is the backend "runner" referenced by the task brief: it only
// runs when floci.enabled is set. A reachability failure is returned so the
// caller can log it, but it does not prevent the stage from retrying later.
func Start(cfg pipeline.FlociConfig) error {
	configMu.Lock()
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.Region == "" {
		cfg.Region = DefaultRegion
	}
	activeCfg = cfg
	configMu.Unlock()

	log.Infof("floci: backend enabled (endpoint=%s region=%s)", cfg.Endpoint, cfg.Region)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clients, err := NewClients(ctx, cfg.Endpoint, cfg.Region)
	if err != nil {
		return err
	}
	return clients.Ping(ctx)
}

// activeConfig returns a snapshot of the current Floci configuration.
func activeConfig() pipeline.FlociConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	return activeCfg
}
