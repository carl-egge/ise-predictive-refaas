package floci

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	rdsdatatypes "github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
	log "github.com/sirupsen/logrus"
)

// RDS Data API setup actions and side-effect checkers. Floci runs this
// against a real backing SQL engine, so setup/checker are both plain SQL
// statements dispatched through ExecuteStatement.
func init() {
	RegisterSetup("rdsdata.execute", SetupFunc(setupRDSDataExecute))

	RegisterChecker("rdsdata.rowExists", CheckerFunc(checkRDSDataRowExists))
}

// rdsDataSpec is the union of fields the RDS Data API handlers understand.
type rdsDataSpec struct {
	ResourceArn string `json:"resourceArn"`
	SecretArn   string `json:"secretArn"`
	Database    string `json:"database"`
	Sql         string `json:"sql"`
	// Attributes, if set, must all be present with matching values in the
	// first row returned by rdsdata.rowExists.
	Attributes map[string]interface{} `json:"attributes"`
}

func decodeRDSDataSpec(spec json.RawMessage) (rdsDataSpec, error) {
	var s rdsDataSpec
	if err := json.Unmarshal(spec, &s); err != nil {
		return s, fmt.Errorf("invalid rdsdata assertion spec: %w", err)
	}
	if s.ResourceArn == "" || s.SecretArn == "" {
		return s, fmt.Errorf("rdsdata assertion requires \"resourceArn\" and \"secretArn\"")
	}
	if s.Sql == "" {
		return s, fmt.Errorf("rdsdata assertion requires a \"sql\" statement")
	}
	return s, nil
}

// setupRDSDataExecute runs an arbitrary SQL statement (DDL/DML) before
// invocation, e.g. to create a table or seed a row.
func setupRDSDataExecute(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeRDSDataSpec(spec)
	if err != nil {
		return err
	}
	if _, err := c.RDSData.ExecuteStatement(ctx, &rdsdata.ExecuteStatementInput{
		ResourceArn: awsString(s.ResourceArn),
		SecretArn:   awsString(s.SecretArn),
		Database:    awsString(s.Database),
		Sql:         awsString(s.Sql),
	}); err != nil {
		return fmt.Errorf("executing statement: %w", err)
	}
	log.Debugf("floci: ran rdsdata setup statement against %q", s.Database)
	return nil
}

// checkRDSDataRowExists runs a SELECT statement and asserts it returned at
// least one row and, if "attributes" is given, that the first row's columns
// match those values.
func checkRDSDataRowExists(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeRDSDataSpec(spec)
	if err != nil {
		return err
	}
	out, err := c.RDSData.ExecuteStatement(ctx, &rdsdata.ExecuteStatementInput{
		ResourceArn:           awsString(s.ResourceArn),
		SecretArn:             awsString(s.SecretArn),
		Database:              awsString(s.Database),
		Sql:                   awsString(s.Sql),
		IncludeResultMetadata: true,
		FormatRecordsAs:       rdsdatatypes.RecordsFormatTypeJson,
	})
	if err != nil {
		return fmt.Errorf("executing statement: %w", err)
	}
	if out.FormattedRecords == nil || *out.FormattedRecords == "[]" {
		return fmt.Errorf("query returned no rows")
	}
	if len(s.Attributes) == 0 {
		return nil
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(*out.FormattedRecords), &rows); err != nil {
		return fmt.Errorf("decoding query result: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("query returned no rows")
	}
	row := rows[0]
	for k, want := range s.Attributes {
		got, ok := row[k]
		if !ok {
			return fmt.Errorf("row is missing column %q", k)
		}
		if !scalarsEqual(want, got) {
			return fmt.Errorf("row column %q = %v, want %v", k, got, want)
		}
	}
	return nil
}
