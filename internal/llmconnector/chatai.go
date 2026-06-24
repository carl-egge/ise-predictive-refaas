package llmconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// ChatAIInvocationClient wraps the Academic Cloud OpenAI-compatible endpoint
// that the user referenced as ACADEMIC_CLOUD_ENDPOINT.
//
// It implements the llmconnector.Client interface.
// All methods are deliberately lightweight, using only net/http and json
// parsing so the code works without extra SDK dependencies.
type ChatAIInvocationClient struct {
	apiKey    string
	endpoint  string
	modelName string
}

// RegisterFactory adds the "chatai" factory to the global llmconnector.Factories
// map. The registration mirrors the pattern used by gemini.go and ollama.go.
func init() {
	RegisterFactory("chatai", func(args map[string]interface{}) (Client, error) {
		c := &ChatAIInvocationClient{}
		if err := c.Configure(args); err != nil {
			return nil, err
		}
		return c, nil
	})
}

// clientName returns the name of the LLM client, which is "chatai" for this implementation.
func (c *ChatAIInvocationClient) ClientName() string {
	return "chatai"
}

// Configure extracts the required and optional configuration values from the
// args map and stores them on the client struct.
func (c *ChatAIInvocationClient) Configure(args map[string]interface{}) error {
	key, ok := args["ACADEMIC_CLOUD_API_KEY"]
	if !ok {
		return fmt.Errorf("ACADEMIC_CLOUD_API_KEY required")
	}
	c.apiKey = key.(string)

	endpoint, ok := args["ACADEMIC_CLOUD_ENDPOINT"]
	if !ok {
		// sensible default for the academic cloud proxy
		c.endpoint = "https://api.academiccloud.de/v1"
	} else {
		c.endpoint = endpoint.(string)
	}

	model, ok := args["ACADEMIC_CLOUD_MODEL"]
	if ok {
		c.modelName = model.(string)
	} else {
		c.modelName = "meta-llama/Llama-3.3-70B-Instruct"
	}
	return nil
}

// Prepare allows the caller to override the model name at runtime.
// It simply stores the new value in c.modelName; no further side‑effects.
func (c *ChatAIInvocationClient) Prepare(args map[string]interface{}) error {
	if args == nil {
		return nil
	}
	if model, ok := args["ACADEMIC_CLOUD_MODEL"]; ok {
		c.modelName = model.(string)
	}
	return nil
}

// InvokeLLM sends the supplied buffer (containing the user prompt) to the
// Academic Cloud model and returns the generated text together with an empty
// Metrics value (the concrete Metrics struct lives in internal/domain)
// – the caller can attach custom metrics if desired.
func (c *ChatAIInvocationClient) InvokeLLM(ctx context.Context, prompt string) (string, domain.Metrics, error) {
	// start := time.Now()

	payload := map[string]interface{}{
		"model": c.modelName,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.1,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", domain.Metrics{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", domain.Metrics{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", domain.Metrics{}, err
	}
	defer resp.Body.Close()

	// Basic metrics collection – all zeroed for now.
	metrics := domain.Metrics{}

	// Parse the JSON response.
	var respMap map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&respMap); err != nil {
		return "", metrics, err
	}

	// Extract the generated content.
	var content string
	if choices, ok := respMap["choices"].([]interface{}); ok && len(choices) > 0 {
		if choiceMap, ok := choices[0].(map[string]interface{}); ok {
			if msgMap, ok := choiceMap["message"].(map[string]interface{}); ok {
				if msgContent, ok := msgMap["content"].(string); ok {
					content = strings.TrimSpace(msgContent)
				}
			}
		}
	}
	return content, metrics, nil
}
