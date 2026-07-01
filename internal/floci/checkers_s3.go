package floci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/sirupsen/logrus"
)

// S3 setup actions and side-effect checkers. These register themselves in
// init() so simply importing the package wires them into the registries.
func init() {
	RegisterSetup("s3.bucket", SetupFunc(setupS3Bucket))
	RegisterSetup("s3.object", SetupFunc(setupS3Object))

	RegisterChecker("s3.objectExists", CheckerFunc(checkS3ObjectExists))
	RegisterChecker("s3.objectContains", CheckerFunc(checkS3ObjectContains))
}

// s3Spec is the union of fields the S3 handlers understand. Each handler reads
// only the fields it needs.
type s3Spec struct {
	Bucket    string `json:"bucket"`
	Key       string `json:"key"`
	Body      string `json:"body"`
	Substring string `json:"substring"`
}

func decodeS3Spec(spec json.RawMessage) (s3Spec, error) {
	var s s3Spec
	if err := json.Unmarshal(spec, &s); err != nil {
		return s, fmt.Errorf("invalid s3 assertion spec: %w", err)
	}
	if s.Bucket == "" {
		return s, fmt.Errorf("s3 assertion requires a \"bucket\"")
	}
	return s, nil
}

// setupS3Bucket creates a bucket (idempotent — an already-existing bucket is
// not an error, which keeps test cases re-runnable against a warm emulator).
func setupS3Bucket(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeS3Spec(spec)
	if err != nil {
		return err
	}
	_, err = c.S3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: awsString(s.Bucket)})
	if err != nil && !isAlreadyOwned(err) {
		return fmt.Errorf("creating bucket %q: %w", s.Bucket, err)
	}
	log.Debugf("floci: ensured s3 bucket %q", s.Bucket)
	return nil
}

// setupS3Object seeds an object so a test can exercise read paths.
func setupS3Object(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeS3Spec(spec)
	if err != nil {
		return err
	}
	if s.Key == "" {
		return fmt.Errorf("s3.object setup requires a \"key\"")
	}
	_, err = c.S3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: awsString(s.Bucket),
		Key:    awsString(s.Key),
		Body:   strings.NewReader(s.Body),
	})
	if err != nil {
		return fmt.Errorf("putting object %s/%s: %w", s.Bucket, s.Key, err)
	}
	log.Debugf("floci: seeded s3 object %s/%s", s.Bucket, s.Key)
	return nil
}

// checkS3ObjectExists asserts that an object is present at bucket/key.
func checkS3ObjectExists(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeS3Spec(spec)
	if err != nil {
		return err
	}
	if s.Key == "" {
		return fmt.Errorf("s3.objectExists requires a \"key\"")
	}
	if _, err := c.S3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: awsString(s.Bucket),
		Key:    awsString(s.Key),
	}); err != nil {
		return fmt.Errorf("object %s/%s not found: %w", s.Bucket, s.Key, err)
	}
	return nil
}

// checkS3ObjectContains asserts the object body contains the given substring.
func checkS3ObjectContains(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeS3Spec(spec)
	if err != nil {
		return err
	}
	if s.Key == "" {
		return fmt.Errorf("s3.objectContains requires a \"key\"")
	}
	if s.Substring == "" {
		return fmt.Errorf("s3.objectContains requires a \"substring\"")
	}
	out, err := c.S3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awsString(s.Bucket),
		Key:    awsString(s.Key),
	})
	if err != nil {
		return fmt.Errorf("reading object %s/%s: %w", s.Bucket, s.Key, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return fmt.Errorf("reading body of %s/%s: %w", s.Bucket, s.Key, err)
	}
	if !strings.Contains(string(body), s.Substring) {
		return fmt.Errorf("object %s/%s body does not contain %q", s.Bucket, s.Key, s.Substring)
	}
	return nil
}

// isAlreadyOwned reports whether an S3 error just means the bucket already
// exists for us, which we treat as success during setup.
func isAlreadyOwned(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "BucketAlreadyOwnedByYou") ||
		strings.Contains(msg, "BucketAlreadyExists")
}
