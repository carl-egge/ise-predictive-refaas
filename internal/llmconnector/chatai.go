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
	ModelName string
	// RequestParams holds the per-task params (set by Prepare) merged into
	// the outbound /chat/completions request body on every InvokeLLM call.
	RequestParams map[string]interface{}
	// OutputSchema holds this task's expected response shape (set by
	// Prepare, from task_args.output_keys), used to request a JSON Schema
	// structured response. Falls back to the generic
	// response_format: json_object mode when no task-specific schema is
	// set. Whether the backend actually enforces the schema depends on the
	// model/proxy's support for OpenAI's Structured Outputs feature.
	OutputSchema OutputSchema
	endpoint     string
	apiKey       string
	client       *http.Client
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

// Configure initializes the underlying HTTP client using connector-level
// config (called once, never per task).
func (cc *ChatAIInvocationClient) Configure(connectorArgs map[string]interface{}) error {
	endpoint, ok := connectorArgs["ACADEMIC_CLOUD_ENDPOINT"]
	if !ok {
		return fmt.Errorf("ACADEMIC_CLOUD_ENDPOINT could not be found in connectorArgs")
	}
	endpointStr, ok := endpoint.(string)
	if !ok {
		return fmt.Errorf("ACADEMIC_CLOUD_ENDPOINT must be a string")
	}

	apiKey, ok := connectorArgs["ACADEMIC_CLOUD_API_KEY"]
	if !ok {
		return fmt.Errorf("ACADEMIC_CLOUD_API_KEY could not be found in connectorArgs")
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

// Prepare sets model-specific runtime options from the merged per-task
// params (called fresh before every InvokeLLM, including retries).
func (cc *ChatAIInvocationClient) Prepare(taskParams map[string]interface{}) error {
	model, ok := taskParams["model_name"].(string)
	if !ok {
		return fmt.Errorf("model_name must be a string")
	}

	nargs := make(map[string]interface{})
	maps.Copy(nargs, taskParams)

	delete(nargs, "model_name")
	delete(nargs, "strategy")    // pipeline-level validation hint, not an API parameter
	delete(nargs, "num_ctx")     // Ollama-specific context window size, not part of the OpenAI-compatible schema
	delete(nargs, "output_keys") // consumed below into OutputSchema, not an API parameter itself

	// Only fill in defaults that weren't explicitly set by the task/pipeline options.
	defaultParams := map[string]interface{}{
		"max_tokens": defaultMaxOutputTokens,
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
	cc.RequestParams = nargs
	cc.OutputSchema = ParseOutputSchema(taskParams["output_keys"])

	return nil
}

// chatCompletionResponse models the subset of the OpenAI-compatible
// /chat/completions response used by InvokeLLM.
type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		// FinishReason distinguishes a complete generation ("stop") from one
		// cut off at max_tokens ("length") - the latter yields a truncated,
		// usually unparseable payload.
		FinishReason string `json:"finish_reason"`
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
	maps.Copy(body, cc.RequestParams)
	if len(cc.OutputSchema) > 0 {
		schemaObj := map[string]interface{}{
			"type":       "object",
			"properties": cc.OutputSchema.JSONSchemaProperties(),
		}
		if required := cc.OutputSchema.RequiredKeys(); len(required) > 0 {
			schemaObj["required"] = required
		}
		body["response_format"] = map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "task_output",
				"schema": schemaObj,
			},
		}
	}

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

	if len(parsed.Choices) == 0 {
		return "", metrics, fmt.Errorf("chatai response contained no choices")
	}
	choice := parsed.Choices[0]
	if choice.FinishReason == "length" {
		return "", metrics, fmt.Errorf("chatai response truncated at max_tokens after %d completion tokens (finish_reason=length) - increase max_tokens for this task", parsed.Usage.CompletionTokens)
	}
	if choice.Message.Content == "" {
		return "", metrics, fmt.Errorf("chatai response contained no content (finish_reason=%s)", choice.FinishReason)
	}

	return choice.Message.Content, metrics, nil
}
