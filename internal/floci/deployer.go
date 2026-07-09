package floci

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	log "github.com/sirupsen/logrus"
)

// deployLambda creates the function if it does not exist, or updates its code
// if it does, then waits until it is Active so it can be invoked. zipBytes is a
// provided.al2 bootstrap ZIP from packageLambda.
func deployLambda(ctx context.Context, c *Clients, name string, zipBytes []byte) error {
	exists, err := functionExists(ctx, c, name)
	if err != nil {
		return err
	}

	if exists {
		log.Debugf("floci: updating existing lambda %q", name)
		if _, err := c.Lambda.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
			FunctionName: awsString(name),
			ZipFile:      zipBytes,
		}); err != nil {
			return fmt.Errorf("floci: updating lambda %q: %w", name, err)
		}
	} else {
		log.Debugf("floci: creating lambda %q", name)
		if _, err := c.Lambda.CreateFunction(ctx, &lambda.CreateFunctionInput{
			FunctionName: awsString(name),
			Role:         awsString(dummyRoleARN(c.Region)),
			Runtime:      lambdatypes.RuntimeProvidedal2,
			Handler:      awsString("bootstrap"),
			PackageType:  lambdatypes.PackageTypeZip,
			Code:         &lambdatypes.FunctionCode{ZipFile: zipBytes},
			Timeout:      aws.Int32(30),
		}); err != nil {
			return fmt.Errorf("floci: creating lambda %q: %w", name, err)
		}
	}

	return waitActive(ctx, c, name)
}

func functionExists(ctx context.Context, c *Clients, name string) (bool, error) {
	_, err := c.Lambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: awsString(name)})
	if err == nil {
		return true, nil
	}
	var notFound *lambdatypes.ResourceNotFoundException
	if errors.As(err, &notFound) {
		return false, nil
	}
	return false, fmt.Errorf("floci: checking lambda %q: %w", name, err)
}

// waitActive polls the function state until it leaves Pending. Floci pulls the
// runtime image and starts a container on first deploy, so this can take a few
// seconds.
func waitActive(ctx context.Context, c *Clients, name string) error {
	deadline := time.Now().Add(60 * time.Second)
	for {
		out, err := c.Lambda.GetFunctionConfiguration(ctx, &lambda.GetFunctionConfigurationInput{
			FunctionName: awsString(name),
		})
		if err != nil {
			return fmt.Errorf("floci: polling lambda %q state: %w", name, err)
		}
		switch out.State {
		case lambdatypes.StateActive, "":
			return nil
		case lambdatypes.StateFailed:
			return fmt.Errorf("floci: lambda %q entered Failed state: %s", name, aws.ToString(out.StateReason))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("floci: lambda %q not Active within timeout (state=%s)", name, out.State)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// invoke calls the function with payload and returns the raw response bytes. A
// function-level error (an unhandled error inside the Lambda) is surfaced as a
// Go error including the runtime's error payload.
func invoke(ctx context.Context, c *Clients, name string, payload []byte) ([]byte, error) {
	out, err := c.Lambda.Invoke(ctx, &lambda.InvokeInput{
		FunctionName: awsString(name),
		Payload:      payload,
	})
	if err != nil {
		return nil, fmt.Errorf("floci: invoking lambda %q: %w", name, err)
	}
	if out.FunctionError != nil && *out.FunctionError != "" {
		return out.Payload, fmt.Errorf("floci: lambda %q returned %s: %s",
			name, *out.FunctionError, string(out.Payload))
	}
	return out.Payload, nil
}
