package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type blockingConverter struct {
	started chan struct{}
}

func (c *blockingConverter) Apply(runner *pipeline.Runner, request *domain.ConversionRequest) error {
	select {
	case <-c.started:
	default:
		close(c.started)
	}
	<-runner.Done()
	return runner.Err()
}

func TestStopHandlerCancelsActiveJobAndWritesMetrics(t *testing.T) {
	started := make(chan struct{})
	blocker := &blockingConverter{started: started}
	pipe := pipeline.NewPipeline(&pipeline.ConversionTask{
		ID:            "blocking",
		Execute:       blocker,
		MaxRetryCount: 1,
	})

	service := &ConverterService{
		converter:    pipeline.NewRunner(context.Background(), pipe, nil),
		requestQueue: make(chan *domain.ConversionRequest, 1),
		requests:     make(map[uuid.UUID]*domain.ConversionRequest),
		results:      make(map[uuid.UUID]*domain.ConversionRequest),
		metrics:      make(map[uuid.UUID]domain.Metrics),
		pendingStops: make(map[uuid.UUID]struct{}),
	}

	req := pipeline.MakeConversionRequest(&domain.DeploymentPackage{
		RootFile:   "package main\nfunc main() {}\n",
		TestFiles:  map[string]string{},
		BuildFiles: map[string]string{},
		BuildCmd:   []string{},
	})
	service.requests[req.Id] = req
	service.requestQueue <- req
	close(service.requestQueue)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() {
		service.Start(ctx)
		close(finished)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline did not start")
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/stop/"+req.Id.String(), nil)
	request = mux.SetURLVars(request, map[string]string{"uuid": req.Id.String()})
	service.stopHandler(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected status: got %d want %d", recorder.Code, http.StatusAccepted)
	}

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not stop after cancellation")
	}

	service.mutex.RLock()
	metric, ok := service.metrics[req.Id]
	service.mutex.RUnlock()
	if !ok {
		t.Fatal("expected metrics for cancelled job")
	}
	if len(metric.Issues) == 0 {
		t.Fatal("expected metrics issues for cancelled job")
	}
	if !errors.Is(req.LastError(), context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", req.LastError())
	}
	if req.Completed {
		t.Fatal("cancelled job should not be marked completed")
	}
}
