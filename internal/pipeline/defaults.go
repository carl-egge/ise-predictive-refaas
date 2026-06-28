package pipeline

import (
	_ "embed"
	"os"

	_ "github.com/joho/godotenv/autoload"
)

// DefaultOllamaAPIURL is the default local Ollama endpoint.
const DefaultOllamaAPIURL = "http://localhost:11434"

// DefaultPipelineYAML contains the embedded default pipeline configuration.
//
//go:embed default.yaml
var DefaultPipelineYAML string

// setOrDefault returns the value of the environment variable key, or
// defaultValue if it is unset or empty.
// Using the lazy autoloader (_ "github.com/joho/godotenv/autoload") gets the env vars loaded
// before this runs, so we can set defaults from env or hardcoded values.
func setOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

// envDefaults reads the environment variables used to configure LLM clients,
// falling back to hardcoded defaults for anything unset. It re-reads the
// environment on every call so a Runner picks up the current values both at
// startup and on every /reconfigure.
func envDefaults() map[string]any {
	return map[string]any{
		"OLLAMA_API_URL":          setOrDefault("OLLAMA_API_URL", DefaultOllamaAPIURL),
		"GEMINI_API_KEY":          setOrDefault("GEMINI_API_KEY", "NOT+SET"),
		"ACADEMIC_CLOUD_ENDPOINT": setOrDefault("ACADEMIC_CLOUD_ENDPOINT", "https://chat-ai.academiccloud.de/v1"),
		"ACADEMIC_CLOUD_API_KEY":  setOrDefault("ACADEMIC_CLOUD_API_KEY", "NOT+SET"),
		"APP_PORT":                setOrDefault("APP_PORT", "8080"),
	}
}
