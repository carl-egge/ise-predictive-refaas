package llmconnector

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	log "github.com/sirupsen/logrus"
)

// defaultMaxOutputTokens caps generation length when a task doesn't set its
// own max_tokens (ChatAI) / num_predict (Ollama). An explicit cap matters:
// it bounds runaway generations, and hitting it surfaces as a detectable
// "length" finish/done reason instead of a silently truncated response.
const defaultMaxOutputTokens = 2 << 14

// Client abstracts calls to an LLM provider.
type Client interface {
	ClientName() string

	// Configure receives connector-level config (API keys, endpoints) and is
	// called once, when the client is built (Runner construction or
	// /reconfigure) — never per task. Implementations typically cache an
	// expensive client/transport here, guarded by a nil check.
	Configure(connectorArgs map[string]interface{}) error

	// Prepare receives per-task params (model name, temperature, etc. — the
	// merged result of a pipeline's options and a task's own task_args) and
	// is called fresh before every LLM invocation, including retries.
	Prepare(taskParams map[string]interface{}) error

	InvokeLLM(ctx context.Context, buf bytes.Buffer) (string, domain.Metrics, error)
}

// Factory constructs a Client with the provided configuration.
type Factory func(map[string]interface{}) (Client, error)

// Factories registers built-in LLM connector factories.
var Factories = map[string]Factory{}

// RegisterFactory registers a factory by name.
func RegisterFactory(name string, factory Factory) {
	if name == "" || factory == nil {
		return
	}
	Factories[name] = factory
}

// LogResponse logs the LLM interaction to a file.
// It uses the model name, prompt, and response to generate a unique filename.
// The filename format is: chatlogs/<model_name>_YYYYMMDDHHMMSS-<short_uuid>.log
func LogResponse(modelName, prompt, response string) {
	// Ensure chatlogs directory exists
	if err := os.MkdirAll("chatlogs", os.ModePerm); err != nil {
		log.Debugf("failed to create chatlogs directory: %v", err)
		return
	}
	// Generate a timestamp and short UUID for the filename
	timestamp := time.Now().Format("2006-01-02_15-04-05") // YYYY-MM-DD_HH-MM-SS
	shortUUID, err := generateShortUUID(12)               // 12-character short UUID
	if err != nil {
		log.Debugf("failed to generate short UUID for chatlog: %v", err)
		return
	}
	fname := fmt.Sprintf("chatlogs/%s_%s_%s.log", timestamp, modelName, shortUUID)
	// Write the prompt and response to the log file
	logf, err := os.OpenFile(fname, os.O_CREATE|os.O_RDWR, 0644)
	written := 0
	if err != nil {
		log.Debugf("failed to open chatlog file: %v", err)
		return
	}
	defer logf.Close()
	_, _ = logf.WriteString("[PROMPT] ----------------------------\n\n")
	wr, _ := logf.WriteString(prompt)
	written += wr
	_, _ = logf.WriteString("\n\n[RESPONSE] ----------------------------\n\n")
	wr, _ = logf.WriteString(response)
	written += wr
	log.Debugf("logged llm response to: %s with %d bytes", fname, written)
}

// generateShortUUID generates a short, URL-safe UUID-like string of given length.
// Uses SHA3-256 and base64 encoding to produce a short, unique string.
func generateShortUUID(length int) (string, error) {
	bytes := make([]byte, 16) // 128 bits
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(bytes)
	encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(hash)))
	base64.RawURLEncoding.Encode(encoded, hash[:])
	if length > len(encoded) {
		return string(encoded), nil // fallback if length is too long
	}
	return string(encoded[:length]), nil
}
