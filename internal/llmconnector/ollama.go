package llmconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/ollama/ollama/api"
)

// OllamaInvocationClient calls the Ollama API and adapts responses to the
// project's Metrics structure.
type OllamaInvocationClient struct {
	ModelName string
	// RequestParams holds the per-task params (set by Prepare) forwarded as
	// Ollama's request "Options" on every InvokeLLM call.
	RequestParams map[string]interface{}
	// OutputSchema holds this task's expected response shape (set by
	// Prepare, from task_args.output_keys). Falls back to the generic
	// llmOutputSchema (any string-keyed object) when no task-specific
	// schema is set.
	OutputSchema OutputSchema
	client       *api.Client
}

func init() {
	RegisterFactory("ollama", func(args map[string]interface{}) (Client, error) {
		oc := &OllamaInvocationClient{}
		if err := oc.Configure(args); err != nil {
			return nil, err
		}
		return oc, nil
	})
}

func (llm *OllamaInvocationClient) ClientName() string {
	return "ollama"
}

// Configure initializes the underlying Ollama client using connector-level
// config (called once, never per task).
func (llm *OllamaInvocationClient) Configure(connectorArgs map[string]interface{}) error {
	if llm.client == nil {
		urlStr, ok := connectorArgs["OLLAMA_API_URL"]
		if !ok {
			return fmt.Errorf("OLLAMA_API_URL could not be found in connectorArgs")
		}

		client := http.Client{}
		url, _ := url.Parse(urlStr.(string))
		apiClient := api.NewClient(url, &client)
		llm.client = apiClient
	}

	return nil
}

// Prepare sets model-specific runtime options from the merged per-task
// params (called fresh before every InvokeLLM, including retries).
func (llm *OllamaInvocationClient) Prepare(taskParams map[string]interface{}) error {
	// Return an error instead of log.Fatal: a missing model_name in one task
	// config must fail that task, not os.Exit the whole service.
	model, ok := taskParams["model_name"].(string)
	if !ok {
		return fmt.Errorf("model_name must be a string")
	}

	nargs := make(map[string]interface{})
	maps.Copy(nargs, taskParams)

	delete(nargs, "model_name")
	delete(nargs, "output_keys")

	defaultParams := map[string]interface{}{
		"max_tokens": 2 << 14,
		"response_format": map[string]interface{}{
			"type": "json_object",
		},
	}
	maps.Insert(nargs, maps.All(defaultParams))

	llm.ModelName = model
	llm.RequestParams = nargs
	llm.OutputSchema = ParseOutputSchema(taskParams["output_keys"])

	return nil
}

// InvokeLLM sends the prompt to Ollama and returns the textual response along
// with timing metrics.
func (llm *OllamaInvocationClient) InvokeLLM(runner context.Context, buf bytes.Buffer) (string, domain.Metrics, error) {
	var metrics domain.Metrics
	if llm.client == nil {
		return "", metrics, fmt.Errorf("LLM client not initialized")
	}

	format := llmOutputSchema
	if len(llm.OutputSchema) > 0 {
		schemaObj := map[string]interface{}{
			"type":       "object",
			"properties": llm.OutputSchema.JSONSchemaProperties(),
		}
		if required := llm.OutputSchema.RequiredKeys(); len(required) > 0 {
			schemaObj["required"] = required
		}
		if schema, err := json.Marshal(schemaObj); err == nil {
			format = schema
		}
	}

	stream := new(bool)
	req := api.GenerateRequest{
		Model:   llm.ModelName,
		Prompt:  buf.String(),
		Stream:  stream,
		Options: llm.RequestParams,
		Format:  format,
	}

	// Buffered: Generate may deliver a response via the callback and then
	// still return an error (e.g. a mid-stream network failure); with an
	// unbuffered channel the goroutine's second send would block forever
	// after the single receive below, leaking the goroutine.
	callback := make(chan api.GenerateResponse, 2)
	deadline, cancel := context.WithDeadline(runner, time.Now().Add(time.Minute*5))
	defer cancel()
	go func() {
		err := llm.client.Generate(deadline, &req, func(gr api.GenerateResponse) error {
			callback <- gr
			return nil
		})
		if err != nil {
			callback <- api.GenerateResponse{
				DoneReason: err.Error(),
			}
		}
	}()

	response := <-callback

	metrics.ConversionTime += response.TotalDuration
	metrics.ConversionPromptTime += response.PromptEvalDuration
	metrics.ConversionEvalTime += response.EvalDuration
	metrics.ConversionPromptTokenCount += response.PromptEvalCount
	metrics.ConversionEvalTokenCount += response.EvalCount

	if response.Response == "" {
		return "", metrics, fmt.Errorf("response is empty - %s", response.DoneReason)
	}

	return response.Response, metrics, nil
}
