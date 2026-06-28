package llmconnector

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestChatAIInvocationClient_LiveInvokeLLM makes a real, billable call
// against the GWDG/AcademicCloud Chat AI endpoint using a small model and
// the shortest viable prompt, to confirm the HTTP request shape, the
// configured endpoint/key, and the model itself all actually work end to
// end. Skips unless ACADEMIC_CLOUD_API_KEY holds a real key.
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
	if !strings.Contains(strings.ToLower(response), "pong") {
		t.Errorf("expected response to contain %q, got: %s", "pong", response)
	}
	t.Logf("chatai response: %s (metrics: %+v)", response, metrics)
}
