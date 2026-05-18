// Package main contains a Gemini client wrapper that uses Google's
// generative AI SDK to produce package text and metrics.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/option"
)

// GeminiInvocationClient wraps the Gemini SDK and exposes the
// LLMInvocationClient interface.
type GeminiInvocationClient struct {
	geminiAPIKey string
	model        string
}

// Configure sets the API key and optionally a model name.
func (g *GeminiInvocationClient) Configure(args map[string]interface{}) error {

	key, err := args["GEMINI_API_KEY"]
	if !err {
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

// InvokeLLM calls Gemini to generate content and returns the response
// text with `Metrics` about the invocation.
func (g *GeminiInvocationClient) InvokeLLM(ctx context.Context, buf bytes.Buffer) (string, Metrics, error) {
	start := time.Now()
	client, err := genai.NewClient(ctx, option.WithAPIKey(g.geminiAPIKey))
	if err != nil {
		return "", Metrics{}, err
	}
	defer client.Close()

	model := client.GenerativeModel(g.model)

	model.ResponseMIMEType = "application/json"
	//TODO: figure out Schema
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"main.go": &genai.Schema{
				Type:     genai.TypeString,
				Nullable: true,
			},
			"go.mod": &genai.Schema{
				Type:     genai.TypeString,
				Nullable: true,
			},
			"main.py": &genai.Schema{
				Type:     genai.TypeString,
				Nullable: true,
			},
		},
	}
	temp := float32(0.1)
	model.Temperature = &temp

	var metrics = Metrics{}

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

// logLLMResponse persists a chatlog of query/response for debugging.
func (g *GeminiInvocationClient) logLLMResponse(args ...string) {
	fhash := []byte(args[0])
	fname := fmt.Sprintf("chatlogs/%s_%8x_%d.log", g.model, sha256.Sum256(fhash), time.Now().UnixMicro())
	logf, err := os.OpenFile(fname,
		os.O_CREATE|os.O_RDWR, 0644)
	defer logf.Close()
	written := 0
	if err == nil {
		_, _ = logf.WriteString("# Query\n\n")
		wr, _ := logf.WriteString(args[2])
		written += wr
		_, _ = logf.WriteString("\n\n# Response\n\n```\n")
		wr, _ = logf.WriteString(args[1])
		written += wr
		_, _ = logf.WriteString("\n```\n")
	}
	log.Debugf("logged llm response to: %s with %d bytes", fname, written)
}
