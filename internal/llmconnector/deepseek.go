package llmconnector

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/ollama/ollama/api"
	log "github.com/sirupsen/logrus"
)

// DeepSeekInvocationClient calls the Ollama/DeepSeek API and converts responses
// into strings plus timing Metrics.
type DeepSeekInvocationClient struct {
	ModelName      string
	RequestOptions map[string]interface{}
	client         *api.Client
}

func init() {
	RegisterFactory("deepseek", func(args map[string]interface{}) (Client, error) {
		dc := &DeepSeekInvocationClient{}
		if err := dc.Configure(args); err != nil {
			return nil, err
		}
		return dc, nil
	})
}

// Configure initializes the underlying API client using args.
func (llm *DeepSeekInvocationClient) Configure(args map[string]interface{}) error {
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

// Prepare sets model-specific options taken from args.
func (llm *DeepSeekInvocationClient) Prepare(args map[string]interface{}) error {
	model, ok := args["model_name"]
	if !ok {
		log.Fatal("model_name must be a string")
		return nil
	}

	nargs := make(map[string]interface{})
	maps.Copy(nargs, args)

	delete(nargs, "model_name")

	defaultParams := map[string]interface{}{
		"max_tokens": 2 << 14,
	}
	maps.Insert(nargs, maps.All(defaultParams))

	llm.ModelName = model.(string)
	llm.RequestOptions = nargs

	return nil
}

// InvokeLLM sends the prompt buffer to the remote API and returns the textual
// response and timing metrics.
func (llm *DeepSeekInvocationClient) InvokeLLM(runner context.Context, buf bytes.Buffer) (string, domain.Metrics, error) {
	var metrics domain.Metrics
	if llm.client == nil {
		return "", metrics, fmt.Errorf("LLM client not initialized")
	}

	stream := new(bool)
	req := api.GenerateRequest{
		Model:   llm.ModelName,
		Prompt:  buf.String(),
		Stream:  stream,
		Options: llm.RequestOptions,
		Format:  llmOutputSchema,
		System:  "Act as an assistant that only provided an answer without any explanation, ever. Just return what the user asked for using the formating rules.",
	}

	callback := make(chan api.GenerateResponse)
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
