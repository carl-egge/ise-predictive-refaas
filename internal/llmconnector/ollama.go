package llmconnector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
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

// Prepare sets model-specific runtime options from args.
func (llm *OllamaInvocationClient) Prepare(args map[string]interface{}) error {
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
		"response_format": map[string]interface{}{
			"type": "json_object",
		},
	}
	maps.Insert(nargs, maps.All(defaultParams))

	llm.ModelName = model.(string)
	llm.RequestOptions = nargs

	return nil
}

// LogResponse writes a chatlog of the prompt and response.
func (llm *OllamaInvocationClient) LogResponse(args ...string) {
	fhash := []byte(args[0])
	fname := fmt.Sprintf("chatlogs/%s_%8x_%d.log", llm.ModelName, sha256.Sum256(fhash), time.Now().UnixMicro())
	logf, err := os.OpenFile(fname, os.O_CREATE|os.O_RDWR, 0644)
	written := 0
	if err != nil {
		log.Debugf("failed to open log file: %v", err)
		return
	}
	defer logf.Close()
	_, _ = logf.WriteString("# Query\n\n")
	wr, _ := logf.WriteString(args[1])
	written += wr
	_, _ = logf.WriteString("\n\n# Response\n\n```\n")
	wr, _ = logf.WriteString(args[2])
	written += wr
	_, _ = logf.WriteString("\n```\n")
	log.Debugf("logged llm response to: %s with %d bytes", fname, written)
}

// InvokeLLM sends the prompt to Ollama and returns the textual response along
// with timing metrics.
func (llm *OllamaInvocationClient) InvokeLLM(runner context.Context, buf bytes.Buffer) (string, domain.Metrics, error) {
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
