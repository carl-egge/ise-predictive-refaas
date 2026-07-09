package llmconnector

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestOllamaInvocationClient_LiveInvokeLLM makes a real call against a
// running Ollama instance using a small model and the shortest viable
// prompt, to confirm the ollama-go SDK, the configured endpoint, and the
// model itself all actually work end to end. Skips if nothing is listening
// at OLLAMA_API_URL (default http://localhost:11434) rather than failing,
// since most environments won't have Ollama running.
func TestOllamaInvocationClient_LiveInvokeLLM(t *testing.T) {
	apiURL := os.Getenv("OLLAMA_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:11434"
	}
	skipUnlessReachable(t, apiURL)

	client := &OllamaInvocationClient{}
	if err := client.Configure(map[string]interface{}{
		"OLLAMA_API_URL": apiURL,
	}); err != nil {
		t.Fatalf("Configure failed: %v", err)
	}
	if err := client.Prepare(map[string]interface{}{
		"model_name": "qwen2.5-coder:3b",
	}); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var prompt bytes.Buffer
	prompt.WriteString(`Return a JSON object with one field whose value is the string "pong".`)

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
	t.Logf("ollama response: %s (metrics: %+v)", response, metrics)
}
