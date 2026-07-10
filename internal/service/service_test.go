package service

import (
	"archive/zip"
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/google/uuid"
)

// buildZip creates a minimal, valid deployment zip for upload tests.
func buildZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"main.py":      "def handler(event, context):\n    return {}",
		"test/t1.json": `{"input":"{}","output":"{}"}`,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("creating zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("writing zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

// newUploadRequest builds a multipart POST / carrying the given zip bytes as
// the "file" field, matching what uploadHandler expects.
func newUploadRequest(t *testing.T, zipData []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "fn.zip")
	if err != nil {
		t.Fatalf("creating form file: %v", err)
	}
	if _, err := fw.Write(zipData); err != nil {
		t.Fatalf("writing form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// TestUploadHandlerRejectsWhenQueueFull guards [F4]: uploadHandler must not
// block the HTTP goroutine indefinitely when the worker's requestQueue is
// full - it should reject the request with 503 + Retry-After instead, and
// must not leave a dangling cancels/status entry for a job that was never
// actually queued.
func TestUploadHandlerRejectsWhenQueueFull(t *testing.T) {
	service := &ConverterService{
		requestQueue: make(chan *queuedConversion, 1),
		results:      make(map[uuid.UUID]*domain.ConversionRequest),
		metrics:      make(map[uuid.UUID]domain.Metrics),
		cancels:      make(map[uuid.UUID]context.CancelFunc),
		status:       make(map[uuid.UUID]jobStatus),
	}
	// fill the queue's only slot so the next send would block
	service.requestQueue <- &queuedConversion{}

	zipData := buildZip(t)
	w := httptest.NewRecorder()
	service.uploadHandler(w, newUploadRequest(t, zipData))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected a Retry-After header on a 503 rejection")
	}
	if len(service.cancels) != 0 {
		t.Errorf("cancels has %d entries, want 0 - rejected job must not leave a dangling cancel", len(service.cancels))
	}
	if len(service.status) != 0 {
		t.Errorf("status has %d entries, want 0 - rejected job must not leave a dangling status", len(service.status))
	}
}
