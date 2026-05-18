// Package main contains an LLM client implementation that uses the
// Ollama/DeepSeek HTTP API. The client adapts API responses into the
// internal metrics and string format expected by the pipeline.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
	log "github.com/sirupsen/logrus"
)

// DeepSeekInvocationClient calls the Ollama/DeepSeek API and converts
// responses into strings plus timing `Metrics`.
type DeepSeekInvocationClient struct {
	ModelName      string
	RequestOptions map[string]interface{}
	client         *api.Client
}

// Configure initializes the underlying API client using `args`.
func (llm *DeepSeekInvocationClient) Configure(args map[string]interface{}) error {
	if llm.client == nil {
		urlStr, err := args["OLLAMA_API_URL"]
		if !err {
			return fmt.Errorf("OLLAMA_API_URL could not be found in args")
		}

		client := http.Client{}
		url, _ := url.Parse(urlStr.(string))
		api_client := api.NewClient(url, &client)
		llm.client = api_client
	}

	return nil
}

// Prepare sets model-specific options taken from `args`.
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

// logLLMResponse persists a human-readable log of query and response.
func (llm *DeepSeekInvocationClient) logLLMResponse(args ...string) {
	fhash := []byte(args[0])
	fname := fmt.Sprintf("chatlogs/%s_%8x_%d.log", llm.ModelName, sha256.Sum256(fhash), time.Now().UnixMicro())
	logf, err := os.OpenFile(fname,
		os.O_CREATE|os.O_RDWR, 0644)
	defer logf.Close()
	written := 0
	if err == nil {
		_, _ = logf.WriteString("# Query\n\n")
		wr, _ := logf.WriteString(args[1])
		written += wr
		_, _ = logf.WriteString("\n\n# Response\n\n```\n")
		wr, _ = logf.WriteString(args[2])
		written += wr
		_, _ = logf.WriteString("\n```\n")
	}
	log.Debugf("logged llm response to: %s with %d bytes", fname, written)
}

// InvokeLLM sends the prompt buffer to the remote API and returns the
// textual response and timing metrics.
func (llm *DeepSeekInvocationClient) InvokeLLM(runner context.Context, buf bytes.Buffer) (string, Metrics, error) {

	var metrics = Metrics{}
	if llm.client == nil {
		return "", metrics, fmt.Errorf("LLM client not initialized")
	}

	steam := new(bool)
	req := api.GenerateRequest{
		Model:   llm.ModelName,
		Prompt:  buf.String(),
		Stream:  steam,
		Options: llm.RequestOptions,
		Format:  llmOutputSchema,
		System:  "Act as an assistant that only provided an answer without any explanation, ever. Just return what the user asked for using the formating rules.",
	}

	callback := make(chan api.GenerateResponse)
	//TODO: make configurable
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

// GoDeepSeekOllamaReader adapts DeepSeek responses into a
// `DeploymentPackage` using the internal `GoJsonOllamaReader`.
type GoDeepSeekOllamaReader struct {
	internal GoJsonOllamaReader
}

// makeDeploymentFile extracts JSON blocks and delegates to the
// internal JSON reader.
func (gr GoDeepSeekOllamaReader) makeDeploymentFile(response string, original *DeploymentPackage) (*DeploymentPackage, error) {
	if response == "" {
		return nil, fmt.Errorf("response is empty")
	}
	content := response
	if strings.Contains(content, "</think>") {
		_, content, _ = strings.Cut(content, "</think>")
	}

	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")

	if start == -1 || end == -1 {
		return nil, fmt.Errorf("response is missing json - %s", content)
	}
	jsonContent := content[start : end+1]
	jsonContent = strings.Replace(jsonContent, "\n", "", -1)
	return gr.internal.makeDeploymentFile(jsonContent, original)
}
