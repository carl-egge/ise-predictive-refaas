// Package floci provides an optional, Floci-backed integration testing stage
// for the translation pipeline. It deploys a translated Go function as a
// Lambda into a local Floci AWS emulator, invokes it with event payloads from
// declarative test cases, and validates both the direct Lambda response and
// any AWS side effects (S3 objects, DynamoDB items, ...).
//
// The whole package is opt-in: nothing here runs unless the pipeline is
// configured with floci.enabled=true (see pipeline.FlociConfig) and a
// "flociTester" task is present in the PipelineFile. When disabled the stage
// is a no-op, so existing translation/validation behavior is untouched.
package floci

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Clients bundles the AWS SDK clients used by the Floci stage, all pointed at
// the local Floci endpoint. A single Clients value is shared by the deployer,
// the setup actions, and the side-effect checkers so they all talk to the same
// emulator.
type Clients struct {
	Lambda   *lambda.Client
	S3       *s3.Client
	DynamoDB *dynamodb.Client
	Region   string
	Endpoint string
}

// NewClients builds AWS SDK v2 clients configured for Floci. Floci accepts any
// non-empty dummy credentials and serves every service from a single endpoint
// (default http://localhost:4566). S3 uses path-style addressing because the
// emulator does not do virtual-host bucket routing.
func NewClients(ctx context.Context, endpoint, region string) (*Clients, error) {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if region == "" {
		region = DefaultRegion
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
		config.WithBaseEndpoint(endpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("floci: loading AWS config: %w", err)
	}

	return &Clients{
		Lambda: lambda.NewFromConfig(cfg),
		S3: s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.UsePathStyle = true
		}),
		DynamoDB: dynamodb.NewFromConfig(cfg),
		Region:   region,
		Endpoint: endpoint,
	}, nil
}

// Ping performs a cheap reachability check against Floci so callers can emit a
// clear "Floci unavailable" error before attempting a deploy. It lists Lambda
// functions, which every Floci instance answers regardless of state.
func (c *Clients) Ping(ctx context.Context) error {
	if _, err := c.Lambda.ListFunctions(ctx, &lambda.ListFunctionsInput{}); err != nil {
		return fmt.Errorf("floci: emulator not reachable at %s: %w", c.Endpoint, err)
	}
	return nil
}

// dummyRoleARN is an IAM role ARN accepted by Floci for Lambda creation. Floci
// does not enforce IAM on Lambda execution, so any well-formed ARN works.
func dummyRoleARN(region string) string {
	_ = region // ARNs for IAM are global; kept for symmetry/readability.
	return "arn:aws:iam::000000000000:role/floci-lambda-role"
}

// awsString is a tiny readability helper around aws.String.
func awsString(s string) *string { return aws.String(s) }
