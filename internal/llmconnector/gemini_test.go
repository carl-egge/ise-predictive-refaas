package llmconnector

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// TestGeminiInvocationClient_LiveInvokeLLM makes a real, billable call
// against the Gemini API using a small model and the shortest viable
// prompt, to confirm the genai SDK, the configured API key, and the model
// itself all actually work end to end. Skips unless GEMINI_API_KEY holds a
// real key.
//
// InvokeLLM forces a fixed response schema with main.go/go.mod/main.py
// string fields (see gemini.go), so the prompt asks the model to put its
// answer in main.go rather than asking it to "say pong" unconstrained.
func TestGeminiInvocationClient_LiveInvokeLLM(t *testing.T) {
	apiKey := skipUnlessSet(t, "GEMINI_API_KEY")

	client := &GeminiInvocationClient{}
	if err := client.Configure(map[string]interface{}{
		"GEMINI_API_KEY": apiKey,
		"GEMINI_MODEL":   "gemini-2.5-flash",
	}); err != nil {
		t.Fatalf("Configure failed: %v", err)
	}
	if err := client.Prepare(map[string]interface{}{
		"GEMINI_MODEL": "gemini-2.5-flash",
	}); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var prompt bytes.Buffer
	prompt.WriteString(`Set the value of main.go to the string "pong" and leave the other fields null.`)

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
	t.Logf("gemini response: %s (metrics: %+v)", response, metrics)
}
