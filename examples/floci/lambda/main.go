// Command lambda is an example side-effecting Go Lambda used to exercise the
// Floci integration stage. It represents the kind of translated function the
// pipeline targets: it does real AWS work (writes a DynamoDB item and an S3
// audit object) so the side-effect checkers have something to assert against.
//
// On an event of shape {"id","name","email"} it:
//   - puts an item into the "Users" DynamoDB table, and
//   - writes "<id>.json" into the "audit" S3 bucket,
//
// then returns {"status":"ok","id":"<id>"}.
//
// The AWS clients are configured from the standard environment
// (AWS_ENDPOINT_URL / AWS_REGION / credentials), which Floci injects into the
// Lambda container so calls loop back to the emulator.
//
// Note: when this function is run *through* the translation pipeline, the Floci
// packager injects its own `func main() { lambda.Start(handle) }` wrapper, so a
// translated package only needs to expose `handle`. The main() below makes this
// example runnable and buildable on its own.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type event struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type response struct {
	Status string `json:"status"`
	ID     string `json:"id"`
}

func handle(ctx context.Context, raw json.RawMessage) (response, error) {
	var e event
	if err := json.Unmarshal(raw, &e); err != nil {
		return response{}, fmt.Errorf("decoding event: %w", err)
	}
	if e.ID == "" {
		return response{}, fmt.Errorf("event is missing \"id\"")
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return response{}, fmt.Errorf("loading aws config: %w", err)
	}
	ddb := dynamodb.NewFromConfig(cfg)
	s3c := s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true })

	item, err := attributevalue.MarshalMap(e)
	if err != nil {
		return response{}, fmt.Errorf("marshalling item: %w", err)
	}
	if _, err := ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("Users"),
		Item:      item,
	}); err != nil {
		return response{}, fmt.Errorf("putting user: %w", err)
	}

	audit, _ := json.Marshal(e)
	if _, err := s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("audit"),
		Key:    aws.String(e.ID + ".json"),
		Body:   bytes.NewReader(audit),
	}); err != nil {
		return response{}, fmt.Errorf("writing audit object: %w", err)
	}

	return response{Status: "ok", ID: e.ID}, nil
}

func main() {
	lambda.Start(handle)
}
