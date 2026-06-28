package llmconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// ChatAIInvocationClient calls the GWDG/AcademicCloud Chat AI service, which
// exposes an OpenAI-compatible chat completions API, and adapts responses to
// the project's Metrics structure.
type ChatAIInvocationClient struct {
	ModelName      string
	RequestOptions map[string]interface{}
	endpoint       string
	apiKey         string
	client         *http.Client
}

func init() {
	RegisterFactory("chatai", func(args map[string]interface{}) (Client, error) {
		cc := &ChatAIInvocationClient{}
		if err := cc.Configure(args); err != nil {
			return nil, err
		}
		return cc, nil
	})
}

func (cc *ChatAIInvocationClient) ClientName() string {
	return "chatai"
}

// Configure initializes the underlying HTTP client using args.
func (cc *ChatAIInvocationClient) Configure(args map[string]interface{}) error {
	endpoint, ok := args["ACADEMIC_CLOUD_ENDPOINT"]
	if !ok {
		return fmt.Errorf("ACADEMIC_CLOUD_ENDPOINT could not be found in args")
	}
	endpointStr, ok := endpoint.(string)
	if !ok {
		return fmt.Errorf("ACADEMIC_CLOUD_ENDPOINT must be a string")
	}

	apiKey, ok := args["ACADEMIC_CLOUD_API_KEY"]
	if !ok {
		return fmt.Errorf("ACADEMIC_CLOUD_API_KEY could not be found in args")
	}
	apiKeyStr, ok := apiKey.(string)
	if !ok {
		return fmt.Errorf("ACADEMIC_CLOUD_API_KEY must be a string")
	}

	cc.endpoint = strings.TrimRight(endpointStr, "/")
	cc.apiKey = apiKeyStr

	if cc.client == nil {
		cc.client = &http.Client{}
	}

	return nil
}

// Prepare sets model-specific runtime options from args.
func (cc *ChatAIInvocationClient) Prepare(args map[string]interface{}) error {
	model, ok := args["model_name"].(string)
	if !ok {
		return fmt.Errorf("model_name must be a string")
	}

	nargs := make(map[string]interface{})
	maps.Copy(nargs, args)

	delete(nargs, "model_name")
	delete(nargs, "strategy") // pipeline-level validation hint, not an API parameter
	delete(nargs, "num_ctx")  // Ollama-specific context window size, not part of the OpenAI-compatible schema

	// Only fill in defaults that weren't explicitly set by the task/pipeline options.
	defaultParams := map[string]interface{}{
		"max_tokens": 2 << 14,
		"response_format": map[string]interface{}{
			"type": "json_object",
		},
	}
	for k, v := range defaultParams {
		if _, ok := nargs[k]; !ok {
			nargs[k] = v
		}
	}

	cc.ModelName = model
	cc.RequestOptions = nargs

	return nil
}

// chatCompletionResponse models the subset of the OpenAI-compatible
// /chat/completions response used by InvokeLLM.
type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// InvokeLLM sends the prompt to the Chat AI /chat/completions endpoint and
// returns the textual response along with timing/token metrics.
func (cc *ChatAIInvocationClient) InvokeLLM(ctx context.Context, buf bytes.Buffer) (string, domain.Metrics, error) {
	var metrics domain.Metrics
	if cc.client == nil {
		return "", metrics, fmt.Errorf("LLM client not initialized")
	}

	body := map[string]interface{}{
		"model": cc.ModelName,
		"messages": []map[string]string{
			{"role": "user", "content": buf.String()},
		},
	}
	maps.Copy(body, cc.RequestOptions)

	payload, err := json.Marshal(body)
	if err != nil {
		return "", metrics, fmt.Errorf("failed to encode chatai request: %w", err)
	}

	deadline, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(deadline, http.MethodPost, cc.endpoint+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", metrics, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+cc.apiKey)

	start := time.Now()
	resp, err := cc.client.Do(req)
	if err != nil {
		return "", metrics, fmt.Errorf("failed to call chatai: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", metrics, fmt.Errorf("failed to read chatai response: %w", err)
	}

	elapsed := time.Since(start)
	metrics.ConversionTime = elapsed
	metrics.ConversionPromptTime = elapsed
	metrics.ConversionEvalTime = elapsed

	var parsed chatCompletionResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", metrics, fmt.Errorf("failed to decode chatai response (status %d): %s", resp.StatusCode, string(respBody))
	}

	if parsed.Error != nil {
		return "", metrics, fmt.Errorf("chatai error (status %d): %s", resp.StatusCode, parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", metrics, fmt.Errorf("chatai request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	metrics.ConversionPromptTokenCount = parsed.Usage.PromptTokens
	metrics.ConversionEvalTokenCount = parsed.Usage.CompletionTokens

	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return "", metrics, fmt.Errorf("chatai response contained no choices")
	}

	return parsed.Choices[0].Message.Content, metrics, nil
}
