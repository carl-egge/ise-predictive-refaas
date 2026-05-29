package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Event struct {
	ID      string `json:"id"`
	Bucket  string `json:"bucket"`
	Key     string `json:"key"`
	Table   string `json:"table"`
	Message string `json:"message"`
}

type Response struct {
	OK bool `json:"ok"`
}

type Order struct {
	ID     string `json:"id" dynamodbav:"id"`
	Status string `json:"status" dynamodbav:"status"`
}

func handler(ctx context.Context, event Event) (map[string]any, error) {
	cfg, err := awsConfig(ctx)
	if err != nil {
		return nil, err
	}

	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})
	dynamoClient := dynamodb.NewFromConfig(cfg)

	payload, err := json.Marshal(map[string]any{
		"id":      event.ID,
		"message": event.Message,
	})
	if err != nil {
		return nil, err
	}

	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(event.Bucket),
		Key:    aws.String(event.Key),
		Body:   readSeekCloser{bytes.NewReader(payload)},
	})
	if err != nil {
		return nil, err
	}

	order := Order{ID: event.ID, Status: "stored"}
	item, err := attributevalue.MarshalMap(order)
	if err != nil {
		return nil, err
	}

	_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(event.Table),
		Item:      item,
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"statusCode": 200,
		"body":       Response{OK: true},
	}, nil
}

func awsConfig(ctx context.Context) (aws.Config, error) {
	endpoint := os.Getenv("AWS_ENDPOINT_URL")
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		if endpoint != "" {
			return aws.Endpoint{URL: endpoint, SigningRegion: region}, nil
		}
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	})

	return config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		config.WithEndpointResolverWithOptions(resolver),
	)
}

type readSeekCloser struct {
	*bytes.Reader
}

func (readSeekCloser) Close() error { return nil }

func main() {
	lambda.Start(handler)
}
