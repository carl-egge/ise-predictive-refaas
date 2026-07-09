package floci

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	log "github.com/sirupsen/logrus"
)

// SQS setup actions and side-effect checkers.
func init() {
	RegisterSetup("sqs.queue", SetupFunc(setupSQSQueue))

	RegisterChecker("sqs.messageReceived", CheckerFunc(checkSQSMessageReceived))
}

// sqsSpec is the union of fields the SQS handlers understand.
type sqsSpec struct {
	QueueName string `json:"queueName"`
	QueueURL  string `json:"queueUrl"`
	Substring string `json:"substring"`
}

func decodeSQSSpec(spec json.RawMessage) (sqsSpec, error) {
	var s sqsSpec
	if err := json.Unmarshal(spec, &s); err != nil {
		return s, fmt.Errorf("invalid sqs assertion spec: %w", err)
	}
	if s.QueueName == "" && s.QueueURL == "" {
		return s, fmt.Errorf("sqs assertion requires a \"queueName\" or \"queueUrl\"")
	}
	return s, nil
}

// resolveQueueURL returns the spec's QueueURL if set, otherwise looks it up by
// QueueName so a test case can refer to a queue by its short name throughout.
func resolveQueueURL(ctx context.Context, c *Clients, s sqsSpec) (string, error) {
	if s.QueueURL != "" {
		return s.QueueURL, nil
	}
	out, err := c.SQS.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: awsString(s.QueueName)})
	if err != nil {
		return "", fmt.Errorf("resolving queue %q: %w", s.QueueName, err)
	}
	return *out.QueueUrl, nil
}

// setupSQSQueue creates a queue (idempotent — CreateQueue on an existing queue
// with the same attributes just returns its URL).
func setupSQSQueue(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeSQSSpec(spec)
	if err != nil {
		return err
	}
	if s.QueueName == "" {
		return fmt.Errorf("sqs.queue setup requires a \"queueName\"")
	}
	if _, err := c.SQS.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: awsString(s.QueueName)}); err != nil {
		return fmt.Errorf("creating queue %q: %w", s.QueueName, err)
	}
	log.Debugf("floci: ensured sqs queue %q", s.QueueName)
	return nil
}

// checkSQSMessageReceived asserts a message is waiting on the queue and, if
// "substring" is given, that its body contains it. Received messages are left
// in the queue (not deleted) since a test case may want to assert on them more
// than once.
func checkSQSMessageReceived(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeSQSSpec(spec)
	if err != nil {
		return err
	}
	queueURL, err := resolveQueueURL(ctx, c, s)
	if err != nil {
		return err
	}
	out, err := c.SQS.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            awsString(queueURL),
		MaxNumberOfMessages: 10,
		VisibilityTimeout:   0,
		WaitTimeSeconds:     5,
	})
	if err != nil {
		return fmt.Errorf("receiving from queue %q: %w", queueURL, err)
	}
	if len(out.Messages) == 0 {
		return fmt.Errorf("no message received on queue %q", queueURL)
	}
	if s.Substring == "" {
		return nil
	}
	for _, m := range out.Messages {
		if m.Body != nil && strings.Contains(*m.Body, s.Substring) {
			return nil
		}
	}
	return fmt.Errorf("none of the %d message(s) on queue %q contain %q", len(out.Messages), queueURL, s.Substring)
}
