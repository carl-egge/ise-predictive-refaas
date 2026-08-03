package llmconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/ollama/ollama/api"
	log "github.com/sirupsen/logrus"
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

// ollamaOptionKeys are the model options Ollama's /api/generate actually
// understands (the practical subset of the Modelfile parameters used here).
// Everything else in the merged task params is either pipeline-level config
// ("strategy", "output_keys") or another backend's vocabulary ("max_tokens",
// "response_format") and must not be forwarded: Ollama only warns and
// ignores unknown option keys, which meant no output-token limit was ever
// actually applied.
var ollamaOptionKeys = map[string]bool{
	"temperature":       true,
	"top_p":             true,
	"top_k":             true,
	"min_p":             true,
	"num_ctx":           true,
	"num_predict":       true,
	"seed":              true,
	"stop":              true,
	"repeat_penalty":    true,
	"repeat_last_n":     true,
	"num_keep":          true,
	"presence_penalty":  true,
	"frequency_penalty": true,
	"mirostat":          true,
	"mirostat_eta":      true,
	"mirostat_tau":      true,
}

// ollamaConsumedKeys are task params this connector consumes itself (or
// deliberately translates) - they are expected in taskParams and not worth a
// "dropping" debug log, unlike genuinely unknown keys.
var ollamaConsumedKeys = map[string]bool{
	"model_name":      true,
	"output_keys":     true,
	"max_tokens":      true,
	"strategy":        true,
	"response_format": true,
}

// Prepare sets model-specific runtime options from the merged per-task
// params (called fresh before every InvokeLLM, including retries). The
// filtering has to live here rather than in Configure: Configure only sees
// the connector-level Args, while these params are per task and can differ
// between stages (per-stage model/temperature overrides).
func (llm *OllamaInvocationClient) Prepare(taskParams map[string]interface{}) error {
	// Return an error instead of log.Fatal: a missing model_name in one task
	// config must fail that task, not os.Exit the whole service.
	model, ok := taskParams["model_name"].(string)
	if !ok {
		return fmt.Errorf("model_name must be a string")
	}

	// Allowlist: forward only options Ollama understands.
	nargs := make(map[string]interface{})
	for key, value := range taskParams {
		if ollamaOptionKeys[key] {
			nargs[key] = value
			continue
		}
		if !ollamaConsumedKeys[key] {
			log.Debugf("ollama: dropping task param %q (not a known Ollama option)", key)
		}
	}
	// Map the OpenAI-style max_tokens onto Ollama's num_predict and always
	// set an explicit output budget, so hitting the limit surfaces as
	// done_reason "length" (see InvokeLLM) instead of silent truncation.
	if _, ok := nargs["num_predict"]; !ok {
		if maxTokens, ok := taskParams["max_tokens"]; ok {
			nargs["num_predict"] = maxTokens
		} else {
			nargs["num_predict"] = defaultMaxOutputTokens
		}
	}

	llm.ModelName = model
	llm.RequestParams = nargs
	llm.OutputSchema = ParseOutputSchema(taskParams["output_keys"])

	return nil
}

// InvokeLLM sends the prompt to Ollama and returns the textual response along
// with timing metrics.
func (llm *OllamaInvocationClient) InvokeLLM(runner context.Context, buf bytes.Buffer) (string, domain.Metrics, error) {
	var metrics domain.Metrics
	// Report the model on every returned Metrics, including error paths: a
	// truncated response still consumed tokens and is still attributed to a
	// stage, so its energy has to be costed with the right coefficients.
	metrics.Model = llm.ModelName
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

	// Generate honors the passed context, so it can be called synchronously;
	// transient failures (connection errors, 429/5xx) are retried here with
	// backoff instead of consuming a task-level retry or triggering an LLM
	// recovery prompt.
	var response api.GenerateResponse
	var lastErr error
	for attempt := 0; attempt < maxLLMAttempts; attempt++ {
		if attempt > 0 {
			log.Debugf("ollama: transient failure, retrying (%d/%d): %v", attempt+1, maxLLMAttempts, lastErr)
			if err := sleepBackoff(runner, attempt-1); err != nil {
				return "", metrics, err
			}
		}
		if err := waitForCallSlot(runner); err != nil {
			return "", metrics, err
		}

		deadline, cancel := context.WithDeadline(runner, time.Now().Add(time.Minute*5))
		response = api.GenerateResponse{}
		err := llm.client.Generate(deadline, &req, func(gr api.GenerateResponse) error {
			response = gr
			return nil
		})
		cancel()
		if err != nil {
			if transientOllamaError(err) {
				lastErr = err
				continue
			}
			return "", metrics, err
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return "", metrics, fmt.Errorf("ollama request failed after %d attempts: %w", maxLLMAttempts, lastErr)
	}

	metrics.ConversionTime += response.TotalDuration
	metrics.ConversionPromptTime += response.PromptEvalDuration
	metrics.ConversionEvalTime += response.EvalDuration
	metrics.ConversionPromptTokenCount += response.PromptEvalCount
	metrics.ConversionEvalTokenCount += response.EvalCount

	if response.Response == "" {
		return "", metrics, fmt.Errorf("response is empty - %s", response.DoneReason)
	}
	// A "length" done reason means the model hit num_predict mid-generation:
	// the payload is cut off (usually unparseable JSON). Fail with a
	// self-describing error instead of letting the reader report a
	// misleading parse failure.
	if response.DoneReason == "length" {
		return "", metrics, fmt.Errorf("response truncated at the output-token limit after %d tokens (done_reason=length) - increase max_tokens/num_predict for this task", response.EvalCount)
	}

	return response.Response, metrics, nil
}

// transientOllamaError reports whether an Ollama SDK error is worth
// retrying: rate limits / server errors, and connection-level failures.
// Cancellations and per-attempt deadline hits are never retried (a second
// five-minute timeout would just burn more wall clock).
func transientOllamaError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var statusErr api.StatusError
	if errors.As(err, &statusErr) {
		return transientHTTPStatus(statusErr.StatusCode)
	}
	// not an HTTP-level error from the API -> connection problem
	return true
}
