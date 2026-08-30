package pipeline

import (
	"fmt"
	"strings"
	"testing"
)

// The service logs its ConverterOptions at startup and on every /reconfigure,
// and that output is captured into files kept as evaluation artifacts, so a
// credential printed here does not stay in one place ([F6]).
func TestConverterOptionsStringRedactsCredentials(t *testing.T) {
	const secret = "sk-live-do-not-log-me"

	opts := ConverterOptions{
		LLMClient: "chatai",
		Args: map[string]any{
			"ACADEMIC_CLOUD_API_KEY":  secret,
			"GEMINI_API_KEY":          secret,
			"ACADEMIC_CLOUD_ENDPOINT": "https://chat-ai.academiccloud.de/v1",
			"APP_PORT":                "8080",
		},
	}

	for _, got := range []string{opts.String(), fmt.Sprintf("%+v", opts), fmt.Sprintf("%v", &opts)} {
		if strings.Contains(got, secret) {
			t.Errorf("credential leaked into formatted options: %s", got)
		}
		if !strings.Contains(got, "chat-ai.academiccloud.de") || !strings.Contains(got, "8080") {
			t.Errorf("non-secret args should survive redaction: %s", got)
		}
		if !strings.Contains(got, "chatai") {
			t.Errorf("client name should survive redaction: %s", got)
		}
	}

	// Redaction must not mutate the caller's map - the Runner consumes these
	// same Args to configure the connector after they have been logged.
	if opts.Args["ACADEMIC_CLOUD_API_KEY"] != secret {
		t.Errorf("String mutated the original Args: %v", opts.Args["ACADEMIC_CLOUD_API_KEY"])
	}
}

func TestIsSecretArgKey(t *testing.T) {
	secret := []string{"ACADEMIC_CLOUD_API_KEY", "GEMINI_API_KEY", "api_key", "AUTH_TOKEN", "client_secret", "DB_PASSWORD"}
	public := []string{"ACADEMIC_CLOUD_ENDPOINT", "OLLAMA_API_URL", "APP_PORT", "model_name", "GEMINI_MODEL"}

	for _, k := range secret {
		if !isSecretArgKey(k) {
			t.Errorf("%q should be treated as a credential", k)
		}
	}
	for _, k := range public {
		if isSecretArgKey(k) {
			t.Errorf("%q should not be redacted", k)
		}
	}
}
