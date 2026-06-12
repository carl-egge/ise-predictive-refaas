package llmconnector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	log "github.com/sirupsen/logrus"
)

// The Scalable AI Accelerator (SAIA) or Chat AI service from AcademicCloud is an alternative LLM backend.
// Its service is OpenAI-compatible. Therefore, similar to OpenAI, we provide the following APIs:
// - `v1/completions` for text generation and completion
// - `v1/chat/completions` for user-assistant conversations
// - `v1/models` for the list of available models

// ChatAIInvocationClient wraps the Open AI SDK and exposes the Client interface.
type ChatAIInvocationClient struct {
	chatAiAPIkey string
	endpoint     string
	model        string
}

func init() {
	RegisterFactory("chatai", func(args map[string]interface{}) (Client, error) {
		aic := &ChatAIInvocationClient{}
		if err := aic.Configure(args); err != nil {
			return nil, err
		}
		return aic, nil
	})
}

// Configure sets the API key, endpoint, and model name.
func (c *ChatAIInvocationClient) Configure(args map[string]interface{}) error {
	key, ok := args["ACADEMIC_CLOUD_API_KEY"]
	if !ok {
		return fmt.Errorf("ACADEMIC_CLOUD_API_KEY required")
	}
	c.chatAiAPIkey = key.(string)

	endpoint, ok := args["ACADEMIC_CLOUD_ENDPOINT"]
	if ok {
		c.endpoint = endpoint.(string)
	} else {
		c.endpoint = "https://chat-ai.academiccloud.de/v1"
	}

	model, ok := args["ACADEMIC_CLOUD_MODEL"]
	if ok {
		c.model = model.(string)
	} else {
		c.model = "qwen3-coder-30b-a3b-instruct"
	}

	return nil
}

// Prepare allows overriding the model via runtime args.
func (c *ChatAIInvocationClient) Prepare(args map[string]interface{}) error {
	if args == nil {
		return nil
	}
	model, ok := args["ACADEMIC_CLOUD_MODEL"]
	if ok {
		c.model = model.(string)
	}
	return nil
}

// InvokeLLM calls the Chat AI service to generate content and returns the response text with
// Metrics about the invocation.
func (c *ChatAIInvocationClient) InvokeLLM(ctx context.Context, buf bytes.Buffer) (string, domain.Metrics, error) {
	start := time.Now()

	// Create the request payload
	payload := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "user", "content": buf.String()},
		},
		"temperature": 0.1,
	}

	// Marshal the payload to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", domain.Metrics{}, err
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", domain.Metrics{}, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.chatAiAPIkey)

	// Create HTTP client and send request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", domain.Metrics{}, err
	}
	defer resp.Body.Close()

	var metrics domain.Metrics

	// Parse response
	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", metrics, err
	}

	// Extract the response text
	content := ""
	if choices, ok := response["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if message, ok := choice["message"].(map[string]interface{}); ok {
				if msgContent, ok := message["content"].(string); ok {
					content = msgContent
				}
			}
		}
	}

	// Calculate metrics
	metrics.ConversionTime = time.Since(start)
	metrics.ConversionPromptTime = time.Since(start)
	metrics.ConversionEvalTime = time.Since(start)

	// For now, we'll set token counts to 0 since we don't have access to usage metadata
	// In a real implementation, you'd parse the usage field from the response
	metrics.ConversionPromptTokenCount = 0
	metrics.ConversionEvalTokenCount = 0

	return strings.TrimSpace(content), metrics, nil
}

// LogResponse persists a chatlog of query/response for debugging.
func (c *ChatAIInvocationClient) LogResponse(args ...string) {
	fhash := []byte(args[0])
	fname := fmt.Sprintf("chatlogs/%s_%8x_%d.log", c.model, sha256.Sum256(fhash), time.Now().UnixMicro())
	logf, err := os.OpenFile(fname, os.O_CREATE|os.O_RDWR, 0644)
	written := 0
	if err != nil {
		log.Debugf("failed to open log file: %v", err)
		return
	}
	defer logf.Close()
	_, _ = logf.WriteString("# Query\n\n")
	wr, _ := logf.WriteString(args[2])
	written += wr
	_, _ = logf.WriteString("\n\n# Response\n\n```\n")
	wr, _ = logf.WriteString(args[1])
	written += wr
	_, _ = logf.WriteString("\n```\n")
	log.Debugf("logged llm response to: %s with %d bytes", fname, written)
}
