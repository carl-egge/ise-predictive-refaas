package floci

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// EnsureLambdaFunction creates or updates the Lambda function in Floci.
func EnsureLambdaFunction(ctx context.Context, clients *AWSClients, cfg Config, functionName string, zipBytes []byte, envVars map[string]string) error {
	_, err := clients.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(functionName)})
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if !errorAs(err, &notFound) {
			return fmt.Errorf("failed to describe lambda: %w", err)
		}
		createInput := &lambda.CreateFunctionInput{
			FunctionName: aws.String(functionName),
			Handler:      aws.String("bootstrap"),
			Role:         aws.String(cfg.LambdaRoleARN),
			Runtime:      types.RuntimeProvidedal2,
			Timeout:      aws.Int32(cfg.LambdaTimeoutSeconds),
			MemorySize:   aws.Int32(cfg.LambdaMemoryMB),
			Code:         &types.FunctionCode{ZipFile: zipBytes},
			Architectures: []types.Architecture{
				architectureFromConfig(cfg.GoArch),
			},
		}
		if len(envVars) > 0 {
			createInput.Environment = &types.Environment{Variables: envVars}
		}
		if _, err := clients.Lambda.CreateFunction(ctx, createInput); err != nil {
			return fmt.Errorf("failed to create lambda: %w", err)
		}
		return waitForFunction(ctx, clients, functionName)
	}

	if _, err := clients.Lambda.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(functionName),
		ZipFile:      zipBytes,
	}); err != nil {
		return fmt.Errorf("failed to update lambda code: %w", err)
	}

	if len(envVars) > 0 {
		if _, err := clients.Lambda.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String(functionName),
			Environment:  &types.Environment{Variables: envVars},
			Timeout:      aws.Int32(cfg.LambdaTimeoutSeconds),
			MemorySize:   aws.Int32(cfg.LambdaMemoryMB),
		}); err != nil {
			return fmt.Errorf("failed to update lambda config: %w", err)
		}
	}

	return waitForFunction(ctx, clients, functionName)
}

// UpdateLambdaEnv updates environment variables for a Lambda function.
func UpdateLambdaEnv(ctx context.Context, clients *AWSClients, functionName string, envVars map[string]string) error {
	if _, err := clients.Lambda.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String(functionName),
		Environment:  &types.Environment{Variables: envVars},
	}); err != nil {
		return fmt.Errorf("failed to update lambda environment: %w", err)
	}
	return waitForFunctionUpdated(ctx, clients, functionName)
}

// InvokeLambda calls the Lambda function with the provided JSON payload.
func InvokeLambda(ctx context.Context, clients *AWSClients, functionName string, payload []byte) ([]byte, error) {
	out, err := clients.Lambda.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(functionName),
		Payload:        payload,
		InvocationType: types.InvocationTypeRequestResponse,
		LogType:        types.LogTypeTail,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to invoke lambda: %w", err)
	}
	if out.FunctionError != nil && *out.FunctionError != "" {
		return nil, fmt.Errorf("lambda function error: %s", *out.FunctionError)
	}
	return out.Payload, nil
}

func waitForFunction(ctx context.Context, clients *AWSClients, functionName string) error {
	waiter := lambda.NewFunctionActiveV2Waiter(clients.Lambda)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return waiter.Wait(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(functionName)}, 2*time.Second)
}

func waitForFunctionUpdated(ctx context.Context, clients *AWSClients, functionName string) error {
	waiter := lambda.NewFunctionUpdatedV2Waiter(clients.Lambda)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return waiter.Wait(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(functionName)}, 2*time.Second)
}

func architectureFromConfig(arch string) types.Architecture {
	switch arch {
	case "arm64":
		return types.ArchitectureArm64
	default:
		return types.ArchitectureX8664
	}
}
