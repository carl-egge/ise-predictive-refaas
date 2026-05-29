package floci

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	s3CreateBucketAction  = "s3/create-bucket"
	s3ObjectExistsCheck   = "s3/object-exists"
	s3ObjectContainsCheck = "s3/object-contains"
)

func init() {
	RegisterSetupAction(s3CreateBucket{})
	RegisterSideEffectChecker(s3ObjectExists{})
	RegisterSideEffectChecker(s3ObjectContains{})
}

// s3CreateBucket ensures a bucket exists before testing.
type s3CreateBucket struct{}

func (s3CreateBucket) Name() string { return s3CreateBucketAction }

func (s3CreateBucket) Run(ctx context.Context, clients *AWSClients, params map[string]any) error {
	bucket, err := requireString(params, "bucket")
	if err != nil {
		return err
	}
	_, err = clients.S3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return nil
	}

	var alreadyOwned *s3types.BucketAlreadyOwnedByYou
	if errorAs(err, &alreadyOwned) {
		return nil
	}
	var alreadyExists *s3types.BucketAlreadyExists
	if errorAs(err, &alreadyExists) {
		return nil
	}
	return fmt.Errorf("create bucket failed: %w", err)
}

// s3ObjectExists validates a bucket key exists.
type s3ObjectExists struct{}

func (s3ObjectExists) Name() string { return s3ObjectExistsCheck }

func (s3ObjectExists) Check(ctx context.Context, clients *AWSClients, params map[string]any) error {
	bucket, err := requireString(params, "bucket")
	if err != nil {
		return err
	}
	key, err := requireString(params, "key")
	if err != nil {
		return err
	}
	_, err = clients.S3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("object not found: %w", err)
	}
	return nil
}

// s3ObjectContains validates a bucket key contains a substring.
type s3ObjectContains struct{}

func (s3ObjectContains) Name() string { return s3ObjectContainsCheck }

func (s3ObjectContains) Check(ctx context.Context, clients *AWSClients, params map[string]any) error {
	bucket, err := requireString(params, "bucket")
	if err != nil {
		return err
	}
	key, err := requireString(params, "key")
	if err != nil {
		return err
	}
	contains, err := requireString(params, "contains")
	if err != nil {
		return err
	}

	resp, err := clients.S3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("failed to read object: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return fmt.Errorf("failed to read object body: %w", err)
	}
	if !strings.Contains(string(body), contains) {
		return fmt.Errorf("object body missing substring: %s", contains)
	}
	return nil
}
