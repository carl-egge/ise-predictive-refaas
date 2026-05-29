package floci

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	dynamoCreateTableAction = "dynamodb/create-table"
	dynamoItemExistsCheck   = "dynamodb/item-exists"
)

func init() {
	RegisterSetupAction(dynamoCreateTable{})
	RegisterSideEffectChecker(dynamoItemExists{})
}

// dynamoCreateTable ensures a table exists for tests.
type dynamoCreateTable struct{}

func (dynamoCreateTable) Name() string { return dynamoCreateTableAction }

func (dynamoCreateTable) Run(ctx context.Context, clients *AWSClients, params map[string]any) error {
	tableName, err := requireString(params, "table")
	if err != nil {
		return err
	}
	hashKey, err := requireString(params, "hash_key")
	if err != nil {
		return err
	}
	hashKeyType := getParamString(params, "hash_key_type")
	if hashKeyType == "" {
		hashKeyType = "S"
	}

	attributeDefinitions := []dynamodbtypes.AttributeDefinition{
		{
			AttributeName: aws.String(hashKey),
			AttributeType: dynamodbtypes.ScalarAttributeType(hashKeyType),
		},
	}
	keySchema := []dynamodbtypes.KeySchemaElement{
		{
			AttributeName: aws.String(hashKey),
			KeyType:       dynamodbtypes.KeyTypeHash,
		},
	}

	rangeKey := getParamString(params, "range_key")
	if rangeKey != "" {
		rangeKeyType := getParamString(params, "range_key_type")
		if rangeKeyType == "" {
			rangeKeyType = "S"
		}
		attributeDefinitions = append(attributeDefinitions, dynamodbtypes.AttributeDefinition{
			AttributeName: aws.String(rangeKey),
			AttributeType: dynamodbtypes.ScalarAttributeType(rangeKeyType),
		})
		keySchema = append(keySchema, dynamodbtypes.KeySchemaElement{
			AttributeName: aws.String(rangeKey),
			KeyType:       dynamodbtypes.KeyTypeRange,
		})
	}

	_, err = clients.Dynamo.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String(tableName),
		AttributeDefinitions: attributeDefinitions,
		KeySchema:            keySchema,
		BillingMode:          dynamodbtypes.BillingModePayPerRequest,
	})
	if err == nil {
		return waitForTable(ctx, clients, tableName)
	}

	var exists *dynamodbtypes.ResourceInUseException
	if errorAs(err, &exists) {
		return waitForTable(ctx, clients, tableName)
	}
	return fmt.Errorf("create table failed: %w", err)
}

// dynamoItemExists validates a DynamoDB item is present and matches attributes.
type dynamoItemExists struct{}

func (dynamoItemExists) Name() string { return dynamoItemExistsCheck }

func (dynamoItemExists) Check(ctx context.Context, clients *AWSClients, params map[string]any) error {
	tableName, err := requireString(params, "table")
	if err != nil {
		return err
	}
	key := getParamMap(params, "key")
	if key == nil {
		return fmt.Errorf("missing required param: key")
	}
	attrs := getParamMap(params, "attributes")

	keyAV, err := attributevalue.MarshalMap(key)
	if err != nil {
		return fmt.Errorf("invalid key: %w", err)
	}

	resp, err := clients.Dynamo.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(tableName),
		Key:       keyAV,
	})
	if err != nil {
		return fmt.Errorf("get item failed: %w", err)
	}
	if len(resp.Item) == 0 {
		return fmt.Errorf("item not found")
	}
	if attrs == nil {
		return nil
	}

	var item map[string]any
	if err := attributevalue.UnmarshalMap(resp.Item, &item); err != nil {
		return fmt.Errorf("failed to decode item: %w", err)
	}

	if !subsetMatch(attrs, item) {
		return fmt.Errorf("item attributes mismatch")
	}
	return nil
}

func waitForTable(ctx context.Context, clients *AWSClients, tableName string) error {
	waiter := dynamodb.NewTableExistsWaiter(clients.Dynamo)
	return waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(tableName)}, 30*time.Second)
}
