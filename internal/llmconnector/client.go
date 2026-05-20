package llmconnector

import (
	"bytes"
	"context"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// Client abstracts calls to an LLM provider.
type Client interface {
	Configure(args map[string]interface{}) error
	Prepare(args map[string]interface{}) error
	InvokeLLM(ctx context.Context, buf bytes.Buffer) (string, domain.Metrics, error)
	LogResponse(args ...string)
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
