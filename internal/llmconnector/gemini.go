package llmconnector

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/google/generative-ai-go/genai"
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
	}
	temp := float32(0.1)
	model.Temperature = &temp

	var metrics domain.Metrics

	resp, err := model.GenerateContent(ctx, genai.Text(buf.String()))
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
