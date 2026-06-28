package main

import (
	"bytes"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/service"
)

// startTestService boots the converter service in the background and blocks
// until it is accepting connections on its hardcoded address.
func startTestService(t *testing.T) {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.MakeConverterService()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("service failed to start: %v", err)
		default:
		}

		resp, err := http.Get("http://localhost:8080/metrics")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("service did not start listening in time")
}

// TestAppStartAndReconfigureWithChatAI boots the service and checks that it
// responds, then reconfigures it using the project's chatai.json example to
// make sure that config still compiles into a valid pipeline/LLM client.
func TestAppStartAndReconfigureWithChatAI(t *testing.T) {
	startTestService(t)

	resp, err := http.Get("http://localhost:8080/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics returned status %d, want %d", resp.StatusCode, http.StatusOK)
	}

	config, err := os.ReadFile("../../scripts/chatai.json")
	if err != nil {
		t.Fatalf("failed to read scripts/chatai.json: %v", err)
	}

	resp, err = http.Post("http://localhost:8080/reconfigure", "application/json", bytes.NewReader(config))
	if err != nil {
		t.Fatalf("POST /reconfigure failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /reconfigure with scripts/chatai.json returned status %d, want %d", resp.StatusCode, http.StatusCreated)
	}
}
