package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/inputhandler"
	"github.com/carl-egge/ise-predictive-refaas/internal/outputhandler"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

// ConverterService manages background conversion jobs and exposes an HTTP interface.
type ConverterService struct {
	converter    *pipeline.Runner
	requestQueue chan *queuedConversion
	results      map[uuid.UUID]*domain.ConversionRequest
	metrics      map[uuid.UUID]domain.Metrics
	// cancels holds the CancelFunc for every queued or in-progress job,
	// keyed by job UUID. An entry exists from upload until the job finishes
	// (success, failure, or cancellation), at which point it is removed.
	cancels map[uuid.UUID]context.CancelFunc
	mutex   sync.RWMutex
	// runnerMu serializes use of the shared Runner between the background
	// worker (Convert) and /reconfigure (Reconfigure), which swaps the
	// runner's pipeline/LLM client and removes its working directory - doing
	// that mid-conversion would be a data race and could delete the build dir
	// out from under a running job. It is never held together with mutex, so
	// the two locks cannot deadlock; a /reconfigure request simply waits for
	// the in-flight conversion to finish.
	runnerMu sync.Mutex
}

// queuedConversion pairs a conversion request with the context that controls
// it, so cancelling that context (via stopHandler) aborts the job whether
// it's still waiting in requestQueue or already running.
type queuedConversion struct {
	ctx     context.Context
	request *domain.ConversionRequest
}

// MakeConverterService constructs and starts the HTTP converter service; it
// blocks by calling http.ListenAndServe.
func MakeConverterService() error {
	options := pipeline.ConverterOptions{}
	converter, err := pipeline.MakeCodeConverter(&options)
	if err != nil {
		return err
	}

	sv := ConverterService{
		converter:    converter,
		requestQueue: make(chan *queuedConversion, 100),
		results:      make(map[uuid.UUID]*domain.ConversionRequest),
		metrics:      make(map[uuid.UUID]domain.Metrics),
		cancels:      make(map[uuid.UUID]context.CancelFunc),
	}

	log.Infof("Starting converter service with options: %+v", options)

	r := mux.NewRouter()
	r.Path("/").Methods(http.MethodPost).HandlerFunc(sv.uploadHandler)
	r.Path("/metrics").Methods(http.MethodGet).HandlerFunc(sv.metricsHandler)
	r.Path("/reconfigure").Methods(http.MethodPost).HandlerFunc(sv.reconfigure)
	r.Path("/stop/{uuid}").Methods(http.MethodPost).HandlerFunc(sv.stopHandler)
	r.Path("/{uuid}").Methods(http.MethodHead, http.MethodGet).HandlerFunc(sv.pollHandler)

	ctx := context.Background()
	go sv.Start(ctx)

	return http.ListenAndServe("0.0.0.0:8080", r)
}

// Start runs the background worker loop that processes queued conversion requests.
func (service *ConverterService) Start(ctx context.Context) {
	for job := range service.requestQueue {
		request := job.request
		log.Infof("starting request for %s", request.Id)
		startTime := time.Now()
		service.runnerMu.Lock()
		err := service.converter.Convert(job.ctx, request)
		service.runnerMu.Unlock()
		endTime := time.Now()

		service.mutex.Lock()
		if cancel, ok := service.cancels[request.Id]; ok {
			cancel()
			delete(service.cancels, request.Id)
		}
		service.mutex.Unlock()

		if err != nil {
			request.Completed = false
			log.Debugf("error converting best n for %s: %v", request.Id, err)
		} else {
			request.Completed = true
			log.Debugf("converting best n for %s took %v", request.Id, endTime.Sub(startTime))
		}

		if request.Metrics != nil {
			request.Metrics.StartTime = startTime
			request.Metrics.EndTime = endTime
			request.Metrics.TotalTime = endTime.Sub(startTime)
			issues := make([]string, 0)
			for _, err := range request.Errors() {
				issues = append(issues, fmt.Sprintf("%v", err))
			}
			request.Metrics.Issues = issues
			service.mutex.Lock()
			service.metrics[request.Id] = *request.Metrics
			service.results[request.Id] = request
			service.mutex.Unlock()
		}
	}
}

// metricsHandler returns JSON metrics for finished jobs.
func (service *ConverterService) metricsHandler(w http.ResponseWriter, r *http.Request) {
	service.mutex.RLock()
	metricsData, err := json.Marshal(service.metrics)
	service.mutex.RUnlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(metricsData)
}

// pollHandler allows clients to check job status or download the converted package.
func (service *ConverterService) pollHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobUUID, err := uuid.Parse(vars["uuid"])
	if err != nil {
		http.Error(w, fmt.Sprintf("uuid error:%+v %+v", vars, err), http.StatusBadRequest)
		return
	}
	service.mutex.RLock()
	resp, ok := service.results[jobUUID]
	service.mutex.RUnlock()

	if r.Method == http.MethodHead {
		if ok {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, fmt.Sprintf("Unsupported method: %s", r.Method), http.StatusMethodNotAllowed)
		return
	}

	if ok {
		defer func() {
			// deleting is a map write and needs the write lock; doing it
			// under RLock can crash the process on concurrent requests.
			service.mutex.Lock()
			delete(service.results, jobUUID)
			service.mutex.Unlock()
		}()
		if resp == nil || resp.WorkingPackage == nil {
			outputhandler.WriteHTTPError(w, fmt.Errorf("no working package for job uuid %s", jobUUID.String()))
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		var buf bytes.Buffer
		if err := outputhandler.WritePackage(&buf, resp.WorkingPackage); err != nil {
			outputhandler.WriteHTTPError(w, err)
			return
		}
		if !resp.Completed {
			w.WriteHeader(http.StatusNotAcceptable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_, _ = w.Write(buf.Bytes())
		return
	}

	http.NotFound(w, r)
}

// uploadHandler accepts a multipart form with a .zip file and enqueues it for conversion.
func (service *ConverterService) uploadHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File not found in request", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if len(fileHeader.Filename) < 4 || fileHeader.Filename[len(fileHeader.Filename)-4:] != ".zip" {
		http.Error(w, "Only .zip files are allowed", http.StatusUnsupportedMediaType)
		return
	}

	fileData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	dp, err := inputhandler.ReadFromBytes(fileData)
	if err != nil {
		// malformed archives (bad zip, multiple root source files) are client
		// errors and should say why instead of a generic 500.
		http.Error(w, fmt.Sprintf("Error reading file: %v", err), http.StatusBadRequest)
		return
	}
	if dp == nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	request := pipeline.MakeConversionRequest(dp)

	jobCtx, cancel := context.WithCancel(context.Background())
	service.mutex.Lock()
	service.cancels[request.Id] = cancel
	service.mutex.Unlock()

	service.requestQueue <- &queuedConversion{ctx: jobCtx, request: request}
	log.Infof("got new conversion request for %s", request.Id)
	// http.Redirect(w, r, fmt.Sprintf("/%s", request.Id.String()), http.StatusCreated)
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(fmt.Sprintf("%s\n", request.Id.String())))
}

// stopHandler cancels a queued or in-progress conversion identified by uuid.
// The pipeline aborts at the next opportunity instead of continuing to spend
// build/test/LLM resources on it; already-finished jobs are not tracked here
// anymore and report 404.
func (service *ConverterService) stopHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobUUID, err := uuid.Parse(vars["uuid"])
	if err != nil {
		http.Error(w, fmt.Sprintf("uuid error:%+v %+v", vars, err), http.StatusBadRequest)
		return
	}

	service.mutex.RLock()
	cancel, ok := service.cancels[jobUUID]
	service.mutex.RUnlock()

	if !ok {
		http.NotFound(w, r)
		return
	}

	cancel()
	log.Infof("cancellation requested for %s", jobUUID)
	w.WriteHeader(http.StatusAccepted)
}

func (service *ConverterService) reconfigure(w http.ResponseWriter, r *http.Request) {
	var options pipeline.ConverterOptions

	if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
		outputhandler.WriteHTTPError(w, fmt.Errorf("error decoding options: %v", err))
		return
	}

	// Reconfigure swaps the runner's pipeline/client and cleans its working
	// dir; runnerMu keeps that from racing an in-flight Convert. The state
	// maps are wiped under the separate state mutex afterwards - the two
	// locks are intentionally never nested.
	service.runnerMu.Lock()
	err := service.converter.Reconfigure(&options)
	service.runnerMu.Unlock()

	service.mutex.Lock()
	service.metrics = make(map[uuid.UUID]domain.Metrics)
	service.results = make(map[uuid.UUID]*domain.ConversionRequest)
	service.mutex.Unlock()

	if err != nil {
		outputhandler.WriteHTTPError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}
