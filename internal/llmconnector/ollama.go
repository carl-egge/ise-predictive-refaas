package llmconnector

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/ollama/ollama/api"
	log "github.com/sirupsen/logrus"
)

// OllamaInvocationClient calls the Ollama API and adapts responses to the
// project's Metrics structure.
type OllamaInvocationClient struct {
	ModelName      string
	RequestOptions map[string]interface{}
	client         *api.Client
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

// clientName returns the name of the LLM client, which is "ollama" for this implementation.
func (llm *OllamaInvocationClient) ClientName() string {
	return "ollama"
}

// Configure initializes the underlying Ollama client using args.
func (llm *OllamaInvocationClient) Configure(args map[string]interface{}) error {
	if llm.client == nil {
		urlStr, ok := args["OLLAMA_API_URL"]
		if !ok {
			return fmt.Errorf("OLLAMA_API_URL could not be found in args")
		}

		client := http.Client{}
		url, _ := url.Parse(urlStr.(string))
		apiClient := api.NewClient(url, &client)
		llm.client = apiClient
	}

	return nil
}

// Prepare sets model-specific runtime options from args
func (llm *OllamaInvocationClient) Prepare(args map[string]interface{}) error {
	model, ok := args["model_name"]
	if !ok {
		log.Fatal("model_name must be provided")
		return fmt.Errorf("model_name is required")
	}

	// Define valid Ollama parameters with their types
	validOllamaParams := map[string]bool{
		"num_ctx":        true, // Context window size
		"repeat_last_n":  true, // Repetition prevention window
		"repeat_penalty": true, // Repetition penalty
		"temperature":    true, // Creativity level
		"seed":           true, // Random seed
		"stop":           true, // Stop sequences
		"num_predict":    true, // Max tokens to generate
		"top_k":          true, // Top-k sampling
		"top_p":          true, // Nucleus sampling
		"min_p":          true, // Minimum probability
		"max_tokens":     true, // Maps to num_predict
	}

	// Filter args to only include Ollama parameters
	ollamaOptions := make(map[string]interface{})

	for key, value := range args {
		if validOllamaParams[key] {
			// Special handling for parameter mapping
			if key == "max_tokens" {
				ollamaOptions["num_predict"] = value
			} else {
				ollamaOptions[key] = value
			}
		}
	}

	// Set reasonable defaults for code translation/repair tasks
	// These defaults prioritize accuracy and determinism over creativity
	defaultParams := map[string]interface{}{
		"temperature":    0.2,  // Lower temperature for more deterministic output
		"top_k":          30,   // Slightly lower than default for more focused output
		"top_p":          0.9,  // Standard nucleus sampling
		"repeat_penalty": 1.1,  // Slight penalty for repetitions
		"num_predict":    4096, // Reasonable max output for code tasks
		"num_ctx":        4096, // Larger context window for code understanding
	}

	// Apply defaults only if not explicitly set
	for key, value := range defaultParams {
		if _, exists := ollamaOptions[key]; !exists {
			ollamaOptions[key] = value
		}
	}

	log.Debugf("Ollama prepared with model (%s) and options: %v", model.(string), ollamaOptions)

	llm.ModelName = model.(string)
	llm.RequestOptions = ollamaOptions

	return nil
}

// InvokeLLM sends the prompt to Ollama and returns the textual response along
// with timing metrics.
func (llm *OllamaInvocationClient) InvokeLLM(ctx context.Context, prompt string) (string, domain.Metrics, error) {
	var metrics domain.Metrics

	if llm.client == nil {
		return "", metrics, fmt.Errorf("LLM client not initialized")
	}

	stream := true

	req := api.GenerateRequest{
		Model:   llm.ModelName,
		Prompt:  prompt,
		Stream:  &stream,
		Options: llm.RequestOptions,
		Format:  llmOutputSchema,
	}

	// log.Debugf("Invoking request to model %s with prompt length: %d", req.Model, len(req.Prompt))

	// Create a new isolated context for this operation
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var responseBuilder strings.Builder
	var finalResponse api.GenerateResponse

	err := llm.client.Generate(ctx, &req, func(gr api.GenerateResponse) error {
		if gr.Done {
			finalResponse = gr
			return nil
		}
		responseBuilder.WriteString(gr.Response)
		return nil
	})

	if err != nil {
		return "", metrics, fmt.Errorf("generate failed: %w", err)
	}

	metrics.ConversionTime = finalResponse.TotalDuration
	metrics.ConversionPromptTime = finalResponse.PromptEvalDuration
	metrics.ConversionEvalTime = finalResponse.EvalDuration
	metrics.ConversionPromptTokenCount = finalResponse.PromptEvalCount
	metrics.ConversionEvalTokenCount = finalResponse.EvalCount

	if responseBuilder.Len() == 0 {
		return "", metrics, fmt.Errorf("empty response received: %s", finalResponse.DoneReason)
	}

	return responseBuilder.String(), metrics, nil
}
