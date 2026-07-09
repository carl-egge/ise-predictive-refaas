package floci

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	log "github.com/sirupsen/logrus"
)

// Kinesis setup actions and side-effect checkers.
func init() {
	RegisterSetup("kinesis.stream", SetupFunc(setupKinesisStream))

	RegisterChecker("kinesis.recordReceived", CheckerFunc(checkKinesisRecordReceived))
}

// kinesisSpec is the union of fields the Kinesis handlers understand.
type kinesisSpec struct {
	StreamName string `json:"streamName"`
	ShardCount int32  `json:"shardCount"`
	Substring  string `json:"substring"`
}

func decodeKinesisSpec(spec json.RawMessage) (kinesisSpec, error) {
	var s kinesisSpec
	if err := json.Unmarshal(spec, &s); err != nil {
		return s, fmt.Errorf("invalid kinesis assertion spec: %w", err)
	}
	if s.StreamName == "" {
		return s, fmt.Errorf("kinesis assertion requires a \"streamName\"")
	}
	return s, nil
}

// setupKinesisStream creates a stream and waits for it to become ACTIVE, since
// GetShardIterator fails on a still-CREATING stream. Existing streams are
// tolerated so cases stay re-runnable.
func setupKinesisStream(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeKinesisSpec(spec)
	if err != nil {
		return err
	}
	shardCount := s.ShardCount
	if shardCount <= 0 {
		shardCount = 1
	}
	_, err = c.Kinesis.CreateStream(ctx, &kinesis.CreateStreamInput{
		StreamName: awsString(s.StreamName),
		ShardCount: &shardCount,
	})
	if err != nil && !strings.Contains(err.Error(), "ResourceInUseException") {
		return fmt.Errorf("creating stream %q: %w", s.StreamName, err)
	}
	if err := waitKinesisActive(ctx, c, s.StreamName); err != nil {
		return err
	}
	log.Debugf("floci: ensured kinesis stream %q", s.StreamName)
	return nil
}

// waitKinesisActive polls stream status until it leaves CREATING.
func waitKinesisActive(ctx context.Context, c *Clients, name string) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := c.Kinesis.DescribeStreamSummary(ctx, &kinesis.DescribeStreamSummaryInput{StreamName: awsString(name)})
		if err != nil {
			return fmt.Errorf("describing stream %q: %w", name, err)
		}
		switch out.StreamDescriptionSummary.StreamStatus {
		case kinesistypes.StreamStatusActive:
			return nil
		case kinesistypes.StreamStatusDeleting:
			return fmt.Errorf("stream %q is being deleted", name)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("stream %q not ACTIVE within timeout (status=%s)", name, out.StreamDescriptionSummary.StreamStatus)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// checkKinesisRecordReceived asserts at least one record is readable from the
// stream (from the trim horizon, so it also catches records written before the
// checker ran) and, if "substring" is given, that some record's data contains
// it.
func checkKinesisRecordReceived(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeKinesisSpec(spec)
	if err != nil {
		return err
	}

	descOut, err := c.Kinesis.DescribeStream(ctx, &kinesis.DescribeStreamInput{StreamName: awsString(s.StreamName)})
	if err != nil {
		return fmt.Errorf("describing stream %q: %w", s.StreamName, err)
	}
	if len(descOut.StreamDescription.Shards) == 0 {
		return fmt.Errorf("stream %q has no shards", s.StreamName)
	}

	total := 0
	for _, shard := range descOut.StreamDescription.Shards {
		iterOut, err := c.Kinesis.GetShardIterator(ctx, &kinesis.GetShardIteratorInput{
			StreamName:        awsString(s.StreamName),
			ShardId:           shard.ShardId,
			ShardIteratorType: kinesistypes.ShardIteratorTypeTrimHorizon,
		})
		if err != nil {
			return fmt.Errorf("getting shard iterator for %q/%s: %w", s.StreamName, *shard.ShardId, err)
		}
		recOut, err := c.Kinesis.GetRecords(ctx, &kinesis.GetRecordsInput{ShardIterator: iterOut.ShardIterator})
		if err != nil {
			return fmt.Errorf("getting records from %q/%s: %w", s.StreamName, *shard.ShardId, err)
		}
		total += len(recOut.Records)
		if s.Substring == "" {
			continue
		}
		for _, rec := range recOut.Records {
			if strings.Contains(string(rec.Data), s.Substring) {
				return nil
			}
		}
	}

	if total == 0 {
		return fmt.Errorf("no records received on stream %q", s.StreamName)
	}
	if s.Substring != "" {
		return fmt.Errorf("none of the %d record(s) on stream %q contain %q", total, s.StreamName, s.Substring)
	}
	return nil
}
