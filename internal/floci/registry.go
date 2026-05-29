package floci

import "context"

// SetupAction runs a pre-test action against Floci resources.
type SetupAction interface {
	Name() string
	Run(ctx context.Context, clients *AWSClients, params map[string]any) error
}

// SideEffectChecker validates resource side effects after invocation.
type SideEffectChecker interface {
	Name() string
	Check(ctx context.Context, clients *AWSClients, params map[string]any) error
}

// OutputValidator validates the Lambda response payload.
type OutputValidator interface {
	Validate(actual []byte, expected any) error
}

var setupRegistry = map[string]SetupAction{}
var checkerRegistry = map[string]SideEffectChecker{}

// RegisterSetupAction adds a setup action by name.
func RegisterSetupAction(action SetupAction) {
	if action == nil {
		return
	}
	setupRegistry[action.Name()] = action
}

// RegisterSideEffectChecker adds a side effect checker by name.
func RegisterSideEffectChecker(checker SideEffectChecker) {
	if checker == nil {
		return
	}
	checkerRegistry[checker.Name()] = checker
}

// GetSetupAction finds a setup action by type name.
func GetSetupAction(name string) (SetupAction, bool) {
	action, ok := setupRegistry[name]
	return action, ok
}

// GetSideEffectChecker finds a side effect checker by type name.
func GetSideEffectChecker(name string) (SideEffectChecker, bool) {
	checker, ok := checkerRegistry[name]
	return checker, ok
}
