package main

import (
	"context"
	"fmt"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
	"github.com/carl-egge/ise-predictive-refaas/internal/floci"
)

// provisioner puts the AWS emulator into the state a function's fixtures
// expect before that function is measured.
//
// Why this is not optional: 40 of the 95 evaluation_set functions declare
// `setup` actions, and invoking one against an empty emulator does not measure
// the function - it measures its error path. That is the *fastest* path
// through the function, so an unprovisioned run does not merely lose the data
// point, it silently biases the Python/Go comparison toward whichever side
// fails faster. The correctness gate in measure.go catches most of these, but
// only by dropping the function; provisioning is what keeps it in the corpus.
//
// Setup runs once per function, not once per invocation. The actions are
// idempotent by construction (create-if-absent, put-item), and the timed
// region must not include provisioning: the measurement replays one payload
// hundreds of times, and a bucket creation charged to invocation 1 would land
// entirely in the startup term of the two-point split.
type provisioner struct {
	clients *floci.Clients
	// applied remembers which cases have been provisioned, so re-measuring a
	// function (or sharing setup across its payloads) does not re-issue the
	// same create calls.
	applied map[string]bool
}

// newProvisioner connects to the emulator. A nil provisioner is returned when
// provisioning is disabled, and its methods are safe to call.
func newProvisioner(ctx context.Context, endpoint, region string) (*provisioner, error) {
	clients, err := floci.NewClients(ctx, endpoint, region)
	if err != nil {
		return nil, fmt.Errorf("configuring AWS clients for the emulator: %w", err)
	}

	// SDK client construction is lazy, so it proves nothing on its own. Probe
	// the emulator now: discovering it is down after an hour of measuring the
	// functions that need no setup would waste the whole run.
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := clients.Ping(probeCtx); err != nil {
		return nil, fmt.Errorf("AWS emulator not reachable at %s: %w", clients.Endpoint, err)
	}

	return &provisioner{clients: clients, applied: map[string]bool{}}, nil
}

// prepare applies every setup action the function's fixtures declare.
//
// Errors name the emulator rather than the function: a setup failure here is
// infrastructure (emulator down, unsupported action type), and reporting it as
// "this function is unmeasurable" would hide a problem affecting the whole
// run behind 40 individual skips.
func (p *provisioner) prepare(ctx context.Context, functionID string, cases []fixture.TestCase) error {
	if p == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	for i, tc := range cases {
		if len(tc.Setup) == 0 {
			continue
		}
		key := fmt.Sprintf("%s/%d", functionID, i)
		if p.applied[key] {
			continue
		}
		if err := floci.ApplySetup(ctx, p.clients, tc.Setup); err != nil {
			return fmt.Errorf("emulator setup for %s case %q: %w", functionID, tc.Name, err)
		}
		p.applied[key] = true
	}
	return nil
}

// needsProvisioning reports whether any of a function's fixtures declare setup
// actions, so the driver can tell a genuinely provisioning-free function from
// one that was skipped because the emulator was unavailable.
func needsProvisioning(cases []fixture.TestCase) bool {
	for _, tc := range cases {
		if len(tc.Setup) > 0 {
			return true
		}
	}
	return false
}
