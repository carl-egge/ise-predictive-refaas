package floci

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/sirupsen/logrus"
)

// RunIntegrationTests executes Floci-backed test suites for the package.
func RunIntegrationTests(ctx context.Context, cfg Config, pkg *domain.DeploymentPackage) error {
	suites, err := LoadSuites(pkg.TestFiles, cfg)
	if err != nil {
		return err
	}
	if len(suites) == 0 {
		logrus.Debug("no floci test suites found")
		return nil
	}

	clients, err := NewAWSClients(ctx, cfg)
	if err != nil {
		return err
	}
	if err := clients.Ping(ctx); err != nil {
		return fmt.Errorf("floci unavailable: %w", err)
	}

	zipBytes, err := BuildLambdaZip(ctx, pkg, cfg)
	if err != nil {
		return fmt.Errorf("failed to build lambda zip: %w", err)
	}

	baseEnv := envFromPackage(pkg.Env)
	if _, ok := baseEnv["AWS_ENDPOINT_URL"]; !ok {
		baseEnv["AWS_ENDPOINT_URL"] = cfg.Endpoint
	}
	if _, ok := baseEnv["AWS_REGION"]; !ok {
		baseEnv["AWS_REGION"] = cfg.Region
	}
	if _, ok := baseEnv["AWS_ACCESS_KEY_ID"]; !ok {
		baseEnv["AWS_ACCESS_KEY_ID"] = "test"
	}
	if _, ok := baseEnv["AWS_SECRET_ACCESS_KEY"]; !ok {
		baseEnv["AWS_SECRET_ACCESS_KEY"] = "test"
	}
	validator := SubsetJSONValidator{}
	currentEnv := baseEnv
	currentFunction := ""

	for _, suite := range suites {
		suiteName := suite.Name
		if suiteName == "" {
			suiteName = "floci-suite"
		}
		functionName := cfg.FunctionName
		if suite.FunctionName != "" {
			functionName = suite.FunctionName
		}
		if functionName != currentFunction {
			if err := EnsureLambdaFunction(ctx, clients, cfg, functionName, zipBytes, baseEnv); err != nil {
				return err
			}
			currentFunction = functionName
			currentEnv = baseEnv
		}

		if err := runSetupActions(ctx, clients, suite.Setup); err != nil {
			return fmt.Errorf("suite %s setup failed: %w", suiteName, err)
		}

		for _, testCase := range suite.Cases {
			caseName := testCase.Name
			if caseName == "" {
				caseName = "floci-case"
			}
			if err := runSetupActions(ctx, clients, testCase.Setup); err != nil {
				return fmt.Errorf("case %s setup failed: %w", caseName, err)
			}

			desiredEnv := mergeEnv(baseEnv, testCase.Env)
			if !reflect.DeepEqual(desiredEnv, currentEnv) {
				if err := UpdateLambdaEnv(ctx, clients, functionName, desiredEnv); err != nil {
					return fmt.Errorf("case %s failed to update env: %w", caseName, err)
				}
				currentEnv = desiredEnv
			}

			payload, err := json.Marshal(testCase.Payload)
			if err != nil {
				return fmt.Errorf("case %s failed to encode payload: %w", caseName, err)
			}

			response, err := InvokeLambda(ctx, clients, functionName, payload)
			if err != nil {
				return fmt.Errorf("case %s invocation failed: %w", caseName, err)
			}

			if err := validator.Validate(response, testCase.Expected); err != nil {
				return fmt.Errorf("case %s output validation failed: %w", caseName, err)
			}

			if err := runSideEffectChecks(ctx, clients, testCase.SideEffects); err != nil {
				return fmt.Errorf("case %s side effects failed: %w", caseName, err)
			}
		}
	}

	return nil
}

func envFromPackage(envVars []string) map[string]string {
	out := make(map[string]string)
	for _, entry := range envVars {
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		out[parts[0]] = parts[1]
	}
	return out
}

func mergeEnv(base map[string]string, overrides map[string]string) map[string]string {
	if len(overrides) == 0 {
		return base
	}
	out := make(map[string]string, len(base)+len(overrides))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func runSetupActions(ctx context.Context, clients *AWSClients, actions []SetupActionDefinition) error {
	for _, action := range actions {
		impl, ok := GetSetupAction(action.Type)
		if !ok {
			return fmt.Errorf("setup action not registered: %s", action.Type)
		}
		params := action.Params
		if params == nil {
			params = map[string]any{}
		}
		if err := impl.Run(ctx, clients, params); err != nil {
			return err
		}
	}
	return nil
}

func runSideEffectChecks(ctx context.Context, clients *AWSClients, assertions []SideEffectAssertion) error {
	for _, assertion := range assertions {
		impl, ok := GetSideEffectChecker(assertion.Type)
		if !ok {
			return fmt.Errorf("side effect checker not registered: %s", assertion.Type)
		}
		params := assertion.Params
		if params == nil {
			params = map[string]any{}
		}
		if err := impl.Check(ctx, clients, params); err != nil {
			return err
		}
	}
	return nil
}
