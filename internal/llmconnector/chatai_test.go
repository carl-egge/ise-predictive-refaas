package llmconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestChatAIInvocationClient_LiveInvokeLLM makes a real, billable call
// against the GWDG/AcademicCloud Chat AI endpoint using a small model and
// the shortest viable prompt, to confirm the HTTP request shape, the
// configured endpoint/key, and the model itself all actually work end to
// end. Skips unless ACADEMIC_CLOUD_API_KEY holds a real key. It only checks
// that the call succeeds and returns something that parses as JSON - an
// exact wording assertion (e.g. requiring the word "pong") is flaky by
// construction, since a small model's phrasing of a correct answer isn't
// guaranteed to match.
func TestChatAIInvocationClient_LiveInvokeLLM(t *testing.T) {
	apiKey := skipUnlessSet(t, "ACADEMIC_CLOUD_API_KEY")
	endpoint := os.Getenv("ACADEMIC_CLOUD_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://chat-ai.academiccloud.de/v1"
	}

	client := &ChatAIInvocationClient{}
	if err := client.Configure(map[string]interface{}{
		"ACADEMIC_CLOUD_ENDPOINT": endpoint,
		"ACADEMIC_CLOUD_API_KEY":  apiKey,
	}); err != nil {
		t.Fatalf("Configure failed: %v", err)
	}
	if err := client.Prepare(map[string]interface{}{
		"model_name": "meta-llama-3.1-8b-instruct",
		"max_tokens": 50,
	}); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var prompt bytes.Buffer
	prompt.WriteString(`Reply with a JSON object whose single value is the string "pong".`)

	response, metrics, err := client.InvokeLLM(ctx, prompt)
	if err != nil {
		t.Fatalf("InvokeLLM failed: %v", err)
	}
	if response == "" {
		t.Fatal("InvokeLLM returned an empty response")
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(response), &parsed); err != nil {
		t.Errorf("expected response to be valid JSON, got %q: %v", response, err)
	}
	t.Logf("chatai response: %s (metrics: %+v)", response, metrics)
}
