package llmconnector

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

// init loads the repo-root .env explicitly, rather than relying on
// godotenv/autoload (which defaults to a ".env" in the process's current
// working directory). "go test" sets that working directory to this
// package's own directory (internal/llmconnector), not the directory the
// test was invoked from, so the plain autoload lookup never finds a .env
// that only exists at the repo root. runtime.Caller(0) gives this source
// file's own path regardless of the process cwd, so we can resolve the repo
// root relative to it instead.
func init() {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	_ = godotenv.Load(filepath.Join(root, ".env"))
}

// skipUnlessSet skips the test unless the named environment variable holds a
// real (non-empty, non-placeholder) value, so these live-API tests are
// opt-in: go test ./... stays green without credentials, but exercises the
// real SDK/endpoint/model once a developer or a gated CI job provides them.
func skipUnlessSet(t *testing.T, key string) string {
	t.Helper()
	val := os.Getenv(key)
	if val == "" || val == "NOT+SET" {
		t.Skipf("%s not set; skipping live integration test", key)
	}
	return val
}

// skipUnlessReachable skips the test if a GET against baseURL doesn't
// succeed within a short timeout (e.g. no local Ollama instance running).
func skipUnlessReachable(t *testing.T, baseURL string) {
	t.Helper()
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL)
	if err != nil {
		t.Skipf("%s not reachable: %v; skipping live integration test", baseURL, err)
	}
	resp.Body.Close()
}
