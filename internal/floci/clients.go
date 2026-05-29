package floci

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// AWSClients bundles AWS service clients configured for Floci.
type AWSClients struct {
	Config aws.Config
	Lambda *lambda.Client
	S3     *s3.Client
	Dynamo *dynamodb.Client
	STS    *sts.Client
}

// NewAWSClients constructs AWS clients pointed at the Floci endpoint.
func NewAWSClients(ctx context.Context, cfg Config) (*AWSClients, error) {
	resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{URL: cfg.Endpoint, SigningRegion: cfg.Region}, nil
	})

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		config.WithEndpointResolverWithOptions(resolver),
	)
	if err != nil {
		return nil, err
	}

	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
	})

	return &AWSClients{
		Config: awsCfg,
		Lambda: lambda.NewFromConfig(awsCfg),
		S3:     s3Client,
		Dynamo: dynamodb.NewFromConfig(awsCfg),
		STS:    sts.NewFromConfig(awsCfg),
	}, nil
}

// Ping verifies Floci is reachable by calling STS.
func (c *AWSClients) Ping(ctx context.Context) error {
	_, err := c.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	return err
}
