package llmconnector

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// TestGeminiInvocationClientPrepareSetsTemperature guards [E3]: Gemini used
// to hardcode temperature=0.1 in InvokeLLM regardless of taskParams, so a
// per-task or retry-bumped "temperature" (the same key ollama/chatai read)
// had no effect on this backend. Prepare must now pick it up, and must leave
// a previously-set value alone when a later call's taskParams omits the key
// (e.g. a task with no explicit "temperature" configured at all).
func TestGeminiInvocationClientPrepareSetsTemperature(t *testing.T) {
	client := &GeminiInvocationClient{Temperature: defaultGeminiTemperature}

	if err := client.Prepare(map[string]interface{}{"temperature": 0.9}); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if client.Temperature != 0.9 {
		t.Errorf("Temperature = %v, want 0.9", client.Temperature)
	}

	if err := client.Prepare(map[string]interface{}{}); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if client.Temperature != 0.9 {
		t.Errorf("Temperature = %v after a call with no temperature key, want unchanged 0.9", client.Temperature)
	}
}

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
