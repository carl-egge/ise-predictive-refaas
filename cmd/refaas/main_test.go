package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	"github.com/carl-egge/ise-predictive-refaas/internal/service"
	"github.com/google/uuid"
)

// TestMain boots the converter service once for the whole package (it binds
// a hardcoded port and has no shutdown hook, so it can't be started per-test)
// and waits until it is accepting connections before running the tests.
func TestMain(m *testing.M) {
	// Don't let the run log write archive files into the package directory:
	// these tests reconfigure the service, which records a boundary marker.
	os.Setenv("RUN_LOG_DIR", "off")

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.MakeConverterService()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			fmt.Printf("service failed to start: %v\n", err)
			os.Exit(1)
		default:
		}

		resp, err := http.Get("http://localhost:8080/metrics")
		if err == nil {
			resp.Body.Close()
			os.Exit(m.Run())
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("service did not start listening in time")
	os.Exit(1)
}

// TestAppStartAndReconfigureWithChatAI checks that the running service
// responds, then reconfigures it using the project's chatai.json example to
// make sure that config still compiles into a valid pipeline/LLM client.
func TestAppStartAndReconfigureWithChatAI(t *testing.T) {
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

// blockingConverter is a test-only pipeline.Converter that signals startedCh
// once running, then blocks until the runner's context is cancelled, so the
// test can deterministically observe /stop/{uuid} taking effect without
// depending on a real LLM/build backend or timing-based sleeps.
type blockingConverter struct {
	startedCh  chan struct{}
	releasedCh chan struct{}
}

func (b *blockingConverter) Apply(runner *pipeline.Runner, _ *domain.ConversionRequest) error {
	close(b.startedCh)
	<-runner.Done()
	err := runner.Err()
	close(b.releasedCh)
	return err
}

// TestStopEndpoint reconfigures the service with a single-task pipeline that
// blocks until cancelled, uploads a job, waits for it to start, then verifies
// POST /stop/{uuid} unblocks it and that stopping an unknown uuid is a 404.
func TestStopEndpoint(t *testing.T) {
	startedCh := make(chan struct{})
	releasedCh := make(chan struct{})
	pipeline.RegisterConverterFactory("testBlockUntilCancelled", func(map[string]interface{}) pipeline.Converter {
		return &blockingConverter{startedCh: startedCh, releasedCh: releasedCh}
	})

	cfg := []byte(`{
		"LLMClient": "ollama",
		"tasks": [
			{"id": "root", "task": "testBlockUntilCancelled", "maxRetryCount": 1}
		]
	}`)
	resp, err := http.Post("http://localhost:8080/reconfigure", "application/json", bytes.NewReader(cfg))
	if err != nil {
		t.Fatalf("POST /reconfigure failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /reconfigure returned status %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	jobID := uploadEmptyJob(t)

	select {
	case <-startedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("conversion never started")
	}

	resp, err = http.Post(fmt.Sprintf("http://localhost:8080/stop/%s", jobID), "application/json", nil)
	if err != nil {
		t.Fatalf("POST /stop/%s failed: %v", jobID, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /stop/%s returned status %d, want %d", jobID, resp.StatusCode, http.StatusAccepted)
	}

	select {
	case <-releasedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("conversion did not observe cancellation within 5s of /stop")
	}

	resp, err = http.Post("http://localhost:8080/stop/"+uuid.New().String(), "application/json", nil)
	if err != nil {
		t.Fatalf("POST /stop/<unknown> failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /stop/<unknown> returned status %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// uploadEmptyJob posts a minimal but valid package to / and returns the job
// uuid from the response body. It has to carry a source file and a fixture:
// uploadHandler now validates packages up front ([C6]), so a truly empty zip
// is rejected with 400 rather than queued.
func uploadEmptyJob(t *testing.T) string {
	t.Helper()

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	for name, content := range map[string]string{
		"main.py":      "def handler(event, context):\n    return {}",
		"test/t1.json": `{"input":"{}","output":"{}"}`,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to build test zip: %v", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "job.zip")
	if err != nil {
		t.Fatalf("failed to create multipart form file: %v", err)
	}
	if _, err := fw.Write(zipBuf.Bytes()); err != nil {
		t.Fatalf("failed to write zip into multipart form: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	resp, err := http.Post("http://localhost:8080/", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("POST / failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST / returned status %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	idBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read upload response body: %v", err)
	}
	return strings.TrimSpace(string(idBytes))
}
