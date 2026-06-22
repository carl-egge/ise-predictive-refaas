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
	geminiAPIKey string
	model        string
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

// Configure sets the API key and optionally a model name.
func (g *GeminiInvocationClient) Configure(args map[string]interface{}) error {
	key, ok := args["GEMINI_API_KEY"]
	if !ok {
		return fmt.Errorf("GEMINI_API_KEY required")
	}
	g.geminiAPIKey = key.(string)

	model, ok := args["GEMINI_MODEL"]
	if ok {
		g.model = model.(string)
	} else {
		g.model = "gemini-2.0-flash"
	}

	return nil
}

// Prepare allows overriding the model via runtime args.
func (g *GeminiInvocationClient) Prepare(args map[string]interface{}) error {
	if args == nil {
		return nil
	}
	model, ok := args["GEMINI_MODEL"]
	if ok {
		g.model = model.(string)
	}
	return nil
}

// InvokeLLM calls Gemini to generate content and returns the response text with
// Metrics about the invocation.
func (g *GeminiInvocationClient) InvokeLLM(ctx context.Context, buf bytes.Buffer) (string, domain.Metrics, error) {
	start := time.Now()
	client, err := genai.NewClient(ctx, option.WithAPIKey(g.geminiAPIKey))
	if err != nil {
		return "", domain.Metrics{}, err
	}
	defer client.Close()

	model := client.GenerativeModel(g.model)

	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"main.go": {
				Type:     genai.TypeString,
				Nullable: true,
			},
			"go.mod": {
				Type:     genai.TypeString,
				Nullable: true,
			},
			"main.py": {
				Type:     genai.TypeString,
				Nullable: true,
			},
		},
	}
	temp := float32(0.1)
	model.Temperature = &temp

	var metrics domain.Metrics

	resp, err := model.GenerateContent(ctx, genai.Text(buf.String()))
	metrics.ConversionTime = time.Since(start)
	metrics.ConversionPromptTime = time.Since(start)
	metrics.ConversionEvalTime = time.Since(start)
	if resp != nil {
		metrics.ConversionPromptTokenCount += int(resp.UsageMetadata.PromptTokenCount)
		metrics.ConversionEvalTokenCount += int(resp.UsageMetadata.TotalTokenCount)
	}
	if err != nil {
		return "", metrics, err
	}

	var outBuf bytes.Buffer
	if resp != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if txt, ok := part.(genai.Text); ok {
				outBuf.WriteString(string(txt))
			}
		}
	}

	out := strings.TrimSpace(outBuf.String())

	return out, metrics, nil
}
