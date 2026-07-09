package floci

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Checker validates a single declarative side effect (an Assertion) against the
// running emulator. Implementations decode the assertion's Spec into their own
// parameter struct and return a descriptive error when the assertion does not
// hold. Register new checkers with RegisterChecker to extend the assertion
// vocabulary without touching the runner.
type Checker interface {
	Check(ctx context.Context, c *Clients, spec json.RawMessage) error
}

// Setup performs a single declarative setup action (creating a bucket/table,
// seeding an item, ...) before a Lambda is invoked. Register with RegisterSetup.
type Setup interface {
	Run(ctx context.Context, c *Clients, spec json.RawMessage) error
}

// CheckerFunc adapts a plain function to the Checker interface.
type CheckerFunc func(ctx context.Context, c *Clients, spec json.RawMessage) error

func (f CheckerFunc) Check(ctx context.Context, c *Clients, spec json.RawMessage) error {
	return f(ctx, c, spec)
}

// SetupFunc adapts a plain function to the Setup interface.
type SetupFunc func(ctx context.Context, c *Clients, spec json.RawMessage) error

func (f SetupFunc) Run(ctx context.Context, c *Clients, spec json.RawMessage) error {
	return f(ctx, c, spec)
}

var (
	checkerRegistry = map[string]Checker{}
	setupRegistry   = map[string]Setup{}
)

// RegisterChecker registers a side-effect checker under an assertion type
// (e.g. "s3.objectExists"). Last registration wins, so a caller can override a
// built-in checker if needed.
func RegisterChecker(typ string, c Checker) {
	if typ == "" || c == nil {
		return
	}
	checkerRegistry[typ] = c
}

// RegisterSetup registers a setup action under an action type
// (e.g. "s3.bucket").
func RegisterSetup(typ string, s Setup) {
	if typ == "" || s == nil {
		return
	}
	setupRegistry[typ] = s
}

// runSetup dispatches one setup action to its registered handler.
func runSetup(ctx context.Context, c *Clients, a Assertion) error {
	s, ok := setupRegistry[a.Type]
	if !ok {
		return fmt.Errorf("floci: no setup action registered for type %q (registered: %s)", a.Type, registeredSetups())
	}
	if err := s.Run(ctx, c, a.Spec); err != nil {
		return fmt.Errorf("floci: setup %q failed: %w", a.Type, err)
	}
	return nil
}

// runChecker dispatches one side-effect assertion to its registered checker.
func runChecker(ctx context.Context, c *Clients, a Assertion) error {
	chk, ok := checkerRegistry[a.Type]
	if !ok {
		return fmt.Errorf("floci: no side-effect checker registered for type %q (registered: %s)", a.Type, registeredCheckers())
	}
	if err := chk.Check(ctx, c, a.Spec); err != nil {
		return fmt.Errorf("floci: side-effect %q failed: %w", a.Type, err)
	}
	return nil
}

func registeredCheckers() string { return joinKeys(checkerRegistry) }
func registeredSetups() string {
	keys := make([]string, 0, len(setupRegistry))
	for k := range setupRegistry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func joinKeys(m map[string]Checker) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
