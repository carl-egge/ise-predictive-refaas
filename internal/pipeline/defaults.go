package pipeline

import _ "embed"

// DefaultOllamaAPIURL is the default local Ollama endpoint.
const DefaultOllamaAPIURL = "http://localhost:11434"

// DefaultPipelineYAML contains the embedded default pipeline configuration.
//
//go:embed default.yaml
var DefaultPipelineYAML string
