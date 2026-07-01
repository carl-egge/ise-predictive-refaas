package floci

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	log "github.com/sirupsen/logrus"
)

// DynamoDB setup actions and side-effect checkers.
func init() {
	RegisterSetup("dynamodb.table", SetupFunc(setupDynamoTable))
	RegisterSetup("dynamodb.item", SetupFunc(setupDynamoItem))

	RegisterChecker("dynamodb.itemExists", CheckerFunc(checkDynamoItemExists))
}

// dynamoSpec is the union of fields the DynamoDB handlers understand.
type dynamoSpec struct {
	Table   string `json:"table"`
	HashKey string `json:"hashKey"`
	// Key identifies the item for getItem / itemExists. Values are plain JSON
	// scalars and are converted to AttributeValues via attributevalue.
	Key map[string]interface{} `json:"key"`
	// Item is a full record for seeding (dynamodb.item setup).
	Item map[string]interface{} `json:"item"`
	// Attributes are expected attribute values asserted on the fetched item
	// (dynamodb.itemExists).
	Attributes map[string]interface{} `json:"attributes"`
}

func decodeDynamoSpec(spec json.RawMessage) (dynamoSpec, error) {
	var s dynamoSpec
	if err := json.Unmarshal(spec, &s); err != nil {
		return s, fmt.Errorf("invalid dynamodb assertion spec: %w", err)
	}
	if s.Table == "" {
		return s, fmt.Errorf("dynamodb assertion requires a \"table\"")
	}
	return s, nil
}

// setupDynamoTable creates a PAY_PER_REQUEST table with a single string hash
// key. Existing tables are tolerated so cases stay re-runnable.
func setupDynamoTable(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeDynamoSpec(spec)
	if err != nil {
		return err
	}
	if s.HashKey == "" {
		s.HashKey = "id"
	}
	_, err = c.DynamoDB.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   awsString(s.Table),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{
			AttributeName: awsString(s.HashKey),
			AttributeType: ddbtypes.ScalarAttributeTypeS,
		}},
		KeySchema: []ddbtypes.KeySchemaElement{{
			AttributeName: awsString(s.HashKey),
			KeyType:       ddbtypes.KeyTypeHash,
		}},
	})
	if err != nil && !strings.Contains(err.Error(), "ResourceInUseException") {
		return fmt.Errorf("creating table %q: %w", s.Table, err)
	}
	log.Debugf("floci: ensured dynamodb table %q (hashKey=%s)", s.Table, s.HashKey)
	return nil
}

// setupDynamoItem seeds a single item.
func setupDynamoItem(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeDynamoSpec(spec)
	if err != nil {
		return err
	}
	if len(s.Item) == 0 {
		return fmt.Errorf("dynamodb.item setup requires an \"item\"")
	}
	av, err := attributevalue.MarshalMap(s.Item)
	if err != nil {
		return fmt.Errorf("marshalling item for %q: %w", s.Table, err)
	}
	if _, err := c.DynamoDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awsString(s.Table),
		Item:      av,
	}); err != nil {
		return fmt.Errorf("putting item into %q: %w", s.Table, err)
	}
	log.Debugf("floci: seeded dynamodb item in %q", s.Table)
	return nil
}

// checkDynamoItemExists asserts an item exists for the given key and, if
// "attributes" is provided, that those attribute values match.
func checkDynamoItemExists(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeDynamoSpec(spec)
	if err != nil {
		return err
	}
	if len(s.Key) == 0 {
		return fmt.Errorf("dynamodb.itemExists requires a \"key\"")
	}
	key, err := attributevalue.MarshalMap(s.Key)
	if err != nil {
		return fmt.Errorf("marshalling key for %q: %w", s.Table, err)
	}
	out, err := c.DynamoDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      awsString(s.Table),
		Key:            key,
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("getting item from %q: %w", s.Table, err)
	}
	if len(out.Item) == 0 {
		return fmt.Errorf("no item in %q for key %v", s.Table, s.Key)
	}

	var item map[string]interface{}
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return fmt.Errorf("decoding item from %q: %w", s.Table, err)
	}
	for k, want := range s.Attributes {
		got, ok := item[k]
		if !ok {
			return fmt.Errorf("item in %q is missing attribute %q", s.Table, k)
		}
		if !scalarsEqual(want, got) {
			return fmt.Errorf("item in %q attribute %q = %v, want %v", s.Table, k, got, want)
		}
	}
	return nil
}

// scalarsEqual compares two JSON-decoded scalars tolerantly. JSON numbers all
// decode to float64, so numeric comparison is direct; everything else is
// compared by its string form to avoid type-mismatch false negatives.
func scalarsEqual(want, got interface{}) bool {
	if wf, ok := want.(float64); ok {
		if gf, ok := got.(float64); ok {
			return wf == gf
		}
	}
	return fmt.Sprintf("%v", want) == fmt.Sprintf("%v", got)
}
