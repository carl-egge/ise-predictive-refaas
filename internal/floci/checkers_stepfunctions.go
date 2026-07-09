package floci

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	log "github.com/sirupsen/logrus"
)

// Step Functions setup actions and side-effect checkers. A translated
// function's business logic starts an execution (StartExecution); the
// checker here doesn't know the execution ARN up front, so it looks up the
// most recently started execution on the state machine instead.
func init() {
	RegisterSetup("sfn.stateMachine", SetupFunc(setupSFNStateMachine))

	RegisterChecker("sfn.executionStatus", CheckerFunc(checkSFNExecutionStatus))
}

// sfnSpec is the union of fields the Step Functions handlers understand.
type sfnSpec struct {
	StateMachineName string          `json:"stateMachineName"`
	StateMachineArn  string          `json:"stateMachineArn"`
	Definition       json.RawMessage `json:"definition"`
	RoleArn          string          `json:"roleArn"`
	// Status is the expected execution status for sfn.executionStatus,
	// defaulting to "SUCCEEDED".
	Status string `json:"status"`
	// Substring, if set, must appear in the execution's output.
	Substring string `json:"substring"`
}

func decodeSFNSpec(spec json.RawMessage) (sfnSpec, error) {
	var s sfnSpec
	if err := json.Unmarshal(spec, &s); err != nil {
		return s, fmt.Errorf("invalid sfn assertion spec: %w", err)
	}
	if s.StateMachineName == "" && s.StateMachineArn == "" {
		return s, fmt.Errorf("sfn assertion requires a \"stateMachineName\" or \"stateMachineArn\"")
	}
	return s, nil
}

// setupSFNStateMachine creates a standard-workflow state machine from an ASL
// definition. Existing state machines with the same name are tolerated so
// cases stay re-runnable.
func setupSFNStateMachine(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeSFNSpec(spec)
	if err != nil {
		return err
	}
	if s.StateMachineName == "" {
		return fmt.Errorf("sfn.stateMachine setup requires a \"stateMachineName\"")
	}
	if len(s.Definition) == 0 {
		return fmt.Errorf("sfn.stateMachine setup requires a \"definition\"")
	}
	roleArn := s.RoleArn
	if roleArn == "" {
		roleArn = dummyRoleARN(c.Region)
	}
	_, err = c.SFN.CreateStateMachine(ctx, &sfn.CreateStateMachineInput{
		Name:       awsString(s.StateMachineName),
		Definition: awsString(string(s.Definition)),
		RoleArn:    awsString(roleArn),
	})
	if err != nil && !strings.Contains(err.Error(), "StateMachineAlreadyExists") {
		return fmt.Errorf("creating state machine %q: %w", s.StateMachineName, err)
	}
	log.Debugf("floci: ensured sfn state machine %q", s.StateMachineName)
	return nil
}

// resolveStateMachineArn returns the spec's ARN if set, otherwise looks it up
// by name via ListStateMachines.
func resolveStateMachineArn(ctx context.Context, c *Clients, s sfnSpec) (string, error) {
	if s.StateMachineArn != "" {
		return s.StateMachineArn, nil
	}
	out, err := c.SFN.ListStateMachines(ctx, &sfn.ListStateMachinesInput{})
	if err != nil {
		return "", fmt.Errorf("listing state machines: %w", err)
	}
	for _, sm := range out.StateMachines {
		if sm.Name != nil && *sm.Name == s.StateMachineName {
			return *sm.StateMachineArn, nil
		}
	}
	return "", fmt.Errorf("no state machine named %q", s.StateMachineName)
}

// checkSFNExecutionStatus asserts the most recently started execution on the
// state machine reached the expected status (default SUCCEEDED) and, if
// "substring" is given, that its output contains it.
func checkSFNExecutionStatus(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeSFNSpec(spec)
	if err != nil {
		return err
	}
	wantStatus := s.Status
	if wantStatus == "" {
		wantStatus = "SUCCEEDED"
	}

	arn, err := resolveStateMachineArn(ctx, c, s)
	if err != nil {
		return err
	}

	listOut, err := c.SFN.ListExecutions(ctx, &sfn.ListExecutionsInput{StateMachineArn: awsString(arn)})
	if err != nil {
		return fmt.Errorf("listing executions for %q: %w", arn, err)
	}
	if len(listOut.Executions) == 0 {
		return fmt.Errorf("no executions found for state machine %q", arn)
	}
	execs := listOut.Executions
	sort.Slice(execs, func(i, j int) bool { return execs[i].StartDate.After(*execs[j].StartDate) })
	latest := execs[0]

	descOut, err := c.SFN.DescribeExecution(ctx, &sfn.DescribeExecutionInput{ExecutionArn: latest.ExecutionArn})
	if err != nil {
		return fmt.Errorf("describing execution %q: %w", *latest.ExecutionArn, err)
	}
	if string(descOut.Status) != wantStatus {
		return fmt.Errorf("execution %q status = %s, want %s", *latest.ExecutionArn, descOut.Status, wantStatus)
	}
	if descOut.Status != sfntypes.ExecutionStatusSucceeded || s.Substring == "" {
		return nil
	}
	if descOut.Output == nil || !strings.Contains(*descOut.Output, s.Substring) {
		return fmt.Errorf("execution %q output does not contain %q", *latest.ExecutionArn, s.Substring)
	}
	return nil
}
