package llmconnector

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOllamaPrepareFiltersOptions guards the E5 behavior: only options
// Ollama understands are forwarded, the OpenAI-style max_tokens is mapped
// onto num_predict, and an explicit output budget is always set.
func TestOllamaPrepareFiltersOptions(t *testing.T) {
	llm := &OllamaInvocationClient{}
	err := llm.Prepare(map[string]interface{}{
		"model_name":  "qwen2.5-coder:3b",
		"strategy":    "json", // pipeline-level, not an Ollama option
		"max_tokens":  1234,   // OpenAI vocabulary -> num_predict
		"temperature": 0.1,
		"top_p":       0.8,
		"num_ctx":     32768,
		"output_keys": map[string]interface{}{"main.go": map[string]interface{}{}},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	for _, banned := range []string{"strategy", "max_tokens", "response_format", "model_name", "output_keys"} {
		if _, ok := llm.RequestParams[banned]; ok {
			t.Errorf("RequestParams must not contain %q", banned)
		}
	}
	for _, kept := range []string{"temperature", "top_p", "num_ctx"} {
		if _, ok := llm.RequestParams[kept]; !ok {
			t.Errorf("RequestParams must keep valid Ollama option %q", kept)
		}
	}
	if got := llm.RequestParams["num_predict"]; got != 1234 {
		t.Errorf("num_predict = %v, want 1234 (mapped from max_tokens)", got)
	}

	// default budget when neither num_predict nor max_tokens is set
	if err := llm.Prepare(map[string]interface{}{"model_name": "m"}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := llm.RequestParams["num_predict"]; got != defaultMaxOutputTokens {
		t.Errorf("num_predict default = %v, want %d", got, defaultMaxOutputTokens)
	}

	// explicit num_predict wins over max_tokens
	if err := llm.Prepare(map[string]interface{}{"model_name": "m", "num_predict": 55, "max_tokens": 99}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := llm.RequestParams["num_predict"]; got != 55 {
		t.Errorf("num_predict = %v, want explicit 55", got)
	}

	// missing model_name is an error, not a fatal exit
	if err := llm.Prepare(map[string]interface{}{}); err == nil {
		t.Error("Prepare without model_name must return an error")
	}
}

// fakeOllama serves a single /api/generate response with the given
// done_reason so truncation handling can be tested without a live model.
func fakeOllama(t *testing.T, response, doneReason string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"model":"m","response":%q,"done":true,"done_reason":%q,"eval_count":42}`, response, doneReason)
	}))
}

func TestOllamaInvokeLLMDetectsTruncation(t *testing.T) {
	server := fakeOllama(t, `{"main.go": "package main`, "length")
	defer server.Close()

	llm := &OllamaInvocationClient{}
	if err := llm.Configure(map[string]interface{}{"OLLAMA_API_URL": server.URL}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if err := llm.Prepare(map[string]interface{}{"model_name": "m"}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	var buf bytes.Buffer
	buf.WriteString("prompt")
	_, _, err := llm.InvokeLLM(context.Background(), buf)
	if err == nil {
		t.Fatal("expected a truncation error for done_reason=length")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error should mention truncation, got: %v", err)
	}
}

func TestOllamaInvokeLLMHappyPath(t *testing.T) {
	server := fakeOllama(t, `{"main.go": "package main"}`, "stop")
	defer server.Close()

	llm := &OllamaInvocationClient{}
	if err := llm.Configure(map[string]interface{}{"OLLAMA_API_URL": server.URL}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if err := llm.Prepare(map[string]interface{}{"model_name": "m"}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	var buf bytes.Buffer
	buf.WriteString("prompt")
	response, _, err := llm.InvokeLLM(context.Background(), buf)
	if err != nil {
		t.Fatalf("InvokeLLM: %v", err)
	}
	if response != `{"main.go": "package main"}` {
		t.Errorf("unexpected response: %q", response)
	}
}

// fakeChatAI serves a single /chat/completions response with the given
// finish_reason.
func fakeChatAI(t *testing.T, content, finishReason string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":%q}],"usage":{"prompt_tokens":10,"completion_tokens":99}}`, content, finishReason)
	}))
}

func TestChatAIInvokeLLMDetectsTruncation(t *testing.T) {
	server := fakeChatAI(t, `{"main.go": "package main`, "length")
	defer server.Close()

	cc := &ChatAIInvocationClient{}
	if err := cc.Configure(map[string]interface{}{
		"ACADEMIC_CLOUD_ENDPOINT": server.URL,
		"ACADEMIC_CLOUD_API_KEY":  "test-key",
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if err := cc.Prepare(map[string]interface{}{"model_name": "m"}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	var buf bytes.Buffer
	buf.WriteString("prompt")
	_, metrics, err := cc.InvokeLLM(context.Background(), buf)
	if err == nil {
		t.Fatal("expected a truncation error for finish_reason=length")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error should mention truncation, got: %v", err)
	}
	if metrics.ConversionEvalTokenCount != 99 {
		t.Errorf("token metrics should still be recorded on truncation, got %d", metrics.ConversionEvalTokenCount)
	}
}

func TestChatAIInvokeLLMHappyPath(t *testing.T) {
	server := fakeChatAI(t, `{"main.go": "package main"}`, "stop")
	defer server.Close()

	cc := &ChatAIInvocationClient{}
	if err := cc.Configure(map[string]interface{}{
		"ACADEMIC_CLOUD_ENDPOINT": server.URL,
		"ACADEMIC_CLOUD_API_KEY":  "test-key",
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if err := cc.Prepare(map[string]interface{}{"model_name": "m"}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	var buf bytes.Buffer
	buf.WriteString("prompt")
	response, _, err := cc.InvokeLLM(context.Background(), buf)
	if err != nil {
		t.Fatalf("InvokeLLM: %v", err)
	}
	if response != `{"main.go": "package main"}` {
		t.Errorf("unexpected response: %q", response)
	}
}
