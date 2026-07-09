package floci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitotypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	log "github.com/sirupsen/logrus"
)

// Cognito setup actions and side-effect checkers.
func init() {
	RegisterSetup("cognito.userPool", SetupFunc(setupCognitoUserPool))
	RegisterSetup("cognito.user", SetupFunc(setupCognitoUser))

	RegisterChecker("cognito.userAttributes", CheckerFunc(checkCognitoUserAttributes))
}

// cognitoSpec is the union of fields the Cognito handlers understand.
type cognitoSpec struct {
	PoolName string `json:"poolName"`
	PoolID   string `json:"poolId"`
	Username string `json:"username"`
	// Attributes seeds attributes on cognito.user, or is asserted against the
	// fetched user's attributes on cognito.userAttributes.
	Attributes map[string]interface{} `json:"attributes"`
}

func decodeCognitoSpec(spec json.RawMessage) (cognitoSpec, error) {
	var s cognitoSpec
	if err := json.Unmarshal(spec, &s); err != nil {
		return s, fmt.Errorf("invalid cognito assertion spec: %w", err)
	}
	if s.PoolName == "" && s.PoolID == "" {
		return s, fmt.Errorf("cognito assertion requires a \"poolName\" or \"poolId\"")
	}
	return s, nil
}

// setupCognitoUserPool creates a user pool if one with the given name doesn't
// already exist (Cognito's CreateUserPool does not enforce unique names, so
// idempotency has to be done by looking it up first).
func setupCognitoUserPool(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeCognitoSpec(spec)
	if err != nil {
		return err
	}
	if s.PoolName == "" {
		return fmt.Errorf("cognito.userPool setup requires a \"poolName\"")
	}
	if _, err := resolvePoolID(ctx, c, s); err == nil {
		log.Debugf("floci: reusing existing cognito user pool %q", s.PoolName)
		return nil
	}
	if _, err := c.Cognito.CreateUserPool(ctx, &cognitoidentityprovider.CreateUserPoolInput{
		PoolName: awsString(s.PoolName),
	}); err != nil {
		return fmt.Errorf("creating user pool %q: %w", s.PoolName, err)
	}
	log.Debugf("floci: created cognito user pool %q", s.PoolName)
	return nil
}

// setupCognitoUser seeds a user in the pool, suppressing the invite message
// (Floci has no mailbox to deliver it to).
func setupCognitoUser(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeCognitoSpec(spec)
	if err != nil {
		return err
	}
	if s.Username == "" {
		return fmt.Errorf("cognito.user setup requires a \"username\"")
	}
	poolID, err := resolvePoolID(ctx, c, s)
	if err != nil {
		return err
	}
	_, err = c.Cognito.AdminCreateUser(ctx, &cognitoidentityprovider.AdminCreateUserInput{
		UserPoolId:     awsString(poolID),
		Username:       awsString(s.Username),
		UserAttributes: attributesToCognito(s.Attributes),
		MessageAction:  cognitotypes.MessageActionTypeSuppress,
	})
	if err != nil && !isCognitoUserExists(err) {
		return fmt.Errorf("creating user %q in pool %q: %w", s.Username, poolID, err)
	}
	log.Debugf("floci: ensured cognito user %q in pool %q", s.Username, poolID)
	return nil
}

// checkCognitoUserAttributes asserts a user exists and, if "attributes" is
// given, that those attribute values match.
func checkCognitoUserAttributes(ctx context.Context, c *Clients, spec json.RawMessage) error {
	s, err := decodeCognitoSpec(spec)
	if err != nil {
		return err
	}
	if s.Username == "" {
		return fmt.Errorf("cognito.userAttributes requires a \"username\"")
	}
	poolID, err := resolvePoolID(ctx, c, s)
	if err != nil {
		return err
	}
	out, err := c.Cognito.AdminGetUser(ctx, &cognitoidentityprovider.AdminGetUserInput{
		UserPoolId: awsString(poolID),
		Username:   awsString(s.Username),
	})
	if err != nil {
		return fmt.Errorf("getting user %q from pool %q: %w", s.Username, poolID, err)
	}

	got := make(map[string]string, len(out.UserAttributes))
	for _, a := range out.UserAttributes {
		if a.Name != nil && a.Value != nil {
			got[*a.Name] = *a.Value
		}
	}
	for k, want := range s.Attributes {
		gv, ok := got[k]
		if !ok {
			return fmt.Errorf("user %q is missing attribute %q", s.Username, k)
		}
		if !scalarsEqual(want, gv) {
			return fmt.Errorf("user %q attribute %q = %v, want %v", s.Username, k, gv, want)
		}
	}
	return nil
}

// resolvePoolID returns the spec's PoolID if set, otherwise looks it up by
// PoolName via ListUserPools.
func resolvePoolID(ctx context.Context, c *Clients, s cognitoSpec) (string, error) {
	if s.PoolID != "" {
		return s.PoolID, nil
	}
	maxResults := int32(60)
	out, err := c.Cognito.ListUserPools(ctx, &cognitoidentityprovider.ListUserPoolsInput{MaxResults: &maxResults})
	if err != nil {
		return "", fmt.Errorf("listing user pools: %w", err)
	}
	for _, p := range out.UserPools {
		if p.Name != nil && *p.Name == s.PoolName {
			return *p.Id, nil
		}
	}
	return "", fmt.Errorf("no user pool named %q", s.PoolName)
}

// attributesToCognito converts a plain JSON attribute map into Cognito's
// name/value attribute list.
func attributesToCognito(attrs map[string]interface{}) []cognitotypes.AttributeType {
	out := make([]cognitotypes.AttributeType, 0, len(attrs))
	for k, v := range attrs {
		out = append(out, cognitotypes.AttributeType{
			Name:  awsString(k),
			Value: awsString(fmt.Sprintf("%v", v)),
		})
	}
	return out
}

// isCognitoUserExists reports whether an error just means the user already
// exists, which we treat as success during setup.
func isCognitoUserExists(err error) bool {
	var exists *cognitotypes.UsernameExistsException
	return errors.As(err, &exists)
}
