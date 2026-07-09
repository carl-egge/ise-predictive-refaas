package llmconnector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/google/generative-ai-go/genai"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// GeminiInvocationClient wraps the Gemini SDK and exposes the Client interface.
type GeminiInvocationClient struct {
	ModelName    string
	geminiAPIKey string
	// OutputSchema holds this task's expected response shape (set by
	// Prepare, from task_args.output_keys). Falls back to the original
	// main.go/go.mod/main.py schema when no task-specific schema is set.
	OutputSchema OutputSchema
	client       *genai.Client
}

func init() {
	RegisterFactory("gemini", func(args map[string]interface{}) (Client, error) {
		gc := &GeminiInvocationClient{}
		if err := gc.Configure(args); err != nil {
			return nil, err
		}
		return gc, nil
	})
}

func (g *GeminiInvocationClient) ClientName() string {
	return "gemini"
}

// Configure sets the API key and optionally a model name from connector-level
// config, and initializes the underlying Gemini client (called once, never
// per task).
func (g *GeminiInvocationClient) Configure(connectorArgs map[string]interface{}) error {
	key, ok := connectorArgs["GEMINI_API_KEY"]
	if !ok {
		return fmt.Errorf("GEMINI_API_KEY required")
	}
	g.geminiAPIKey = key.(string)

	model, ok := connectorArgs["GEMINI_MODEL"]
	if ok {
		g.ModelName = model.(string)
	} else {
		g.ModelName = "gemini-2.5-flash"
	}

	if g.client == nil {
		client, err := genai.NewClient(context.Background(), option.WithAPIKey(g.geminiAPIKey))
		if err != nil {
			return fmt.Errorf("failed to create gemini client: %w", err)
		}
		g.client = client
	}

	return nil
}

// Prepare allows overriding the model and response schema via per-task
// params (called fresh before every InvokeLLM, including retries).
func (g *GeminiInvocationClient) Prepare(taskParams map[string]interface{}) error {
	if taskParams == nil {
		return nil
	}
	model, ok := taskParams["GEMINI_MODEL"]
	if ok {
		g.ModelName = model.(string)
	}
	g.OutputSchema = ParseOutputSchema(taskParams["output_keys"])
	return nil
}

// InvokeLLM calls Gemini to generate content and returns the response text with
// Metrics about the invocation.
func (g *GeminiInvocationClient) InvokeLLM(ctx context.Context, buf bytes.Buffer) (string, domain.Metrics, error) {
	if g.client == nil {
		return "", domain.Metrics{}, fmt.Errorf("gemini client not initialized")
	}

	start := time.Now()
	model := g.client.GenerativeModel(g.ModelName)

	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type:       genai.TypeObject,
		Properties: g.responseProperties(),
		Required:   g.requiredKeys(),
	}
	temp := float32(0.1)
	model.Temperature = &temp

	var metrics domain.Metrics

	// Transient failures (connection errors, 429/5xx) are retried here with
	// backoff instead of consuming a task-level retry or triggering an LLM
	// recovery prompt.
	var resp *genai.GenerateContentResponse
	var err error
	for attempt := 0; attempt < maxLLMAttempts; attempt++ {
		if attempt > 0 {
			log.Debugf("gemini: transient failure, retrying (%d/%d): %v", attempt+1, maxLLMAttempts, err)
			if serr := sleepBackoff(ctx, attempt-1); serr != nil {
				return "", metrics, serr
			}
		}
		if werr := waitForCallSlot(ctx); werr != nil {
			return "", metrics, werr
		}
		resp, err = model.GenerateContent(ctx, genai.Text(buf.String()))
		if err == nil || !transientGeminiError(err) {
			break
		}
	}
	metrics.ConversionTime = time.Since(start)
	metrics.ConversionPromptTime = time.Since(start)
	metrics.ConversionEvalTime = time.Since(start)
	if resp != nil && resp.UsageMetadata != nil {
		metrics.ConversionPromptTokenCount += int(resp.UsageMetadata.PromptTokenCount)
		metrics.ConversionEvalTokenCount += int(resp.UsageMetadata.CandidatesTokenCount)
	}
	if err != nil {
		return "", metrics, err
	}
	// A safety-blocked or otherwise empty response has no candidates (or a
	// nil content); indexing it unchecked panics and aborts the whole job.
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return "", metrics, fmt.Errorf("gemini response contained no candidates (possibly blocked or empty)")
	}
	if resp.Candidates[0].FinishReason == genai.FinishReasonMaxTokens {
		return "", metrics, fmt.Errorf("gemini response truncated at the output-token limit (finish_reason=MAX_TOKENS)")
	}

	var outBuf bytes.Buffer
	for _, part := range resp.Candidates[0].Content.Parts {
		if txt, ok := part.(genai.Text); ok {
			outBuf.WriteString(string(txt))
		}
	}

	out := strings.TrimSpace(outBuf.String())

	return out, metrics, nil
}

// responseProperties returns this task's expected response fields (from
// OutputSchema, set by Prepare), falling back to the original
// main.go/go.mod/main.py shape when no task-specific schema was set.
func (g *GeminiInvocationClient) responseProperties() map[string]*genai.Schema {
	if len(g.OutputSchema) == 0 {
		return map[string]*genai.Schema{
			"main.go": {Type: genai.TypeString, Nullable: true},
			"go.mod":  {Type: genai.TypeString, Nullable: true},
			"main.py": {Type: genai.TypeString, Nullable: true},
		}
	}
	props := make(map[string]*genai.Schema, len(g.OutputSchema))
	for key, field := range g.OutputSchema {
		props[key] = &genai.Schema{Type: genaiType(field.Type), Nullable: field.Nullable}
	}
	return props
}

// transientGeminiError reports whether a Gemini SDK error is worth
// retrying: rate limits / server errors, and connection-level failures.
// Cancellations and deadline hits are never retried.
func transientGeminiError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return transientHTTPStatus(gerr.Code)
	}
	// not an HTTP-level error from the API -> connection problem
	return true
}

// requiredKeys returns the non-nullable field names of the task schema, or
// nil for the legacy fallback shape (whose fields are all nullable by
// design, since a response carries either main.go or main.py, not both).
func (g *GeminiInvocationClient) requiredKeys() []string {
	if len(g.OutputSchema) == 0 {
		return nil
	}
	return g.OutputSchema.RequiredKeys()
}

// genaiType maps an OutputField's JSON-schema-style type name to Gemini's
// typed schema enum, defaulting to TypeString for anything unrecognized.
func genaiType(t string) genai.Type {
	switch t {
	case "object":
		return genai.TypeObject
	case "array":
		return genai.TypeArray
	case "integer":
		return genai.TypeInteger
	case "number":
		return genai.TypeNumber
	case "boolean":
		return genai.TypeBoolean
	default:
		return genai.TypeString
	}
}
