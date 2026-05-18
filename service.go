// Package main exposes a small HTTP service that accepts zip packages,
// enqueues conversion requests and exposes results and metrics.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

// ConverterService manages background conversion jobs and exposes an
// HTTP interface for submitting and retrieving results.
type ConverterService struct {
	converter    *PipelineRunner
	requestQueue chan *ConversionRequest
	results      map[uuid.UUID]*ConversionRequest
	metrics      map[uuid.UUID]Metrics
	mutex        sync.RWMutex
}

func setOrDefault(key, defaultvalue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultvalue
}

func setFileFromEnv(key, defaultvalue string) string {
	if val := os.Getenv(key); val != "" {
		fs, err := os.OpenFile(val, os.O_RDONLY, 0644)
		if err != nil {
			return defaultvalue
		}
		defer fs.Close()
		fsdat, err := io.ReadAll(fs)
		if err != nil {
			return defaultvalue
		}
		if len(fsdat) == 0 {
			return defaultvalue
		}
		return string(fsdat)
	}
	return defaultvalue
}

// MakeConverterService constructs and starts the HTTP converter
// service; it blocks by calling `http.ListenAndServe`.
func MakeConverterService() error {
	options := DefaultOptions
	options.Args["OLLAMA_API_URL"] = setOrDefault("OLLAMA_API_URL", OLLAMA_API_URL)
	options.Args["GEMINI_API_KEY"] = setOrDefault("GEMINI_API_KEY", "NOT+SET")

	converter, err := MakeCodeConverter(&options)
	if err != nil {
		return err
	}

	sv := ConverterService{
		converter:    converter,
		requestQueue: make(chan *ConversionRequest, 100),
		results:      make(map[uuid.UUID]*ConversionRequest),
		metrics:      make(map[uuid.UUID]Metrics),
	}

	log.Infof("Starting converter service with options: %+v", options)

	r := mux.NewRouter()
	r.Path("/").Methods(http.MethodPost).HandlerFunc(sv.uploadHandler)
	r.Path("/metrics").Methods(http.MethodGet).HandlerFunc(sv.metricsHandler)
	r.Path("/reconfigure").Methods(http.MethodPost).HandlerFunc(sv.reconfigure)
	r.Path("/{uuid}").Methods(http.MethodHead, http.MethodGet).HandlerFunc(sv.pollHandler)

	ctx := context.Background()
	go sv.Start(ctx)

	return http.ListenAndServe("0.0.0.0:8080", r)
}

// Start runs the background worker loop that processes queued
// conversion requests.
func (service *ConverterService) Start(ctx context.Context) {
	for request := range service.requestQueue {
		log.Infof("starting request for %s", request.Id)
		startTime := time.Now()
		err := service.converter.Convert(request)
		endTime := time.Now()
		if err != nil {
			request.Completed = false
			log.Debugf("error converting best n for %s: %v", request.Id, err)
		} else {
			request.Completed = true
			log.Debugf("converting best n for %s took %v", request.Id, endTime.Sub(startTime))
		}

		request.Metrics.StartTime = startTime
		request.Metrics.EndTime = endTime
		request.Metrics.TotalTime = endTime.Sub(startTime)
		issues := make([]string, 0)
		for _, err := range request.err {
			issues = append(issues, fmt.Sprintf("%v", err))
		}
		request.Metrics.Issues = issues
		service.mutex.Lock()
		service.metrics[request.Id] = *request.Metrics
		service.results[request.Id] = request
		service.mutex.Unlock()
	}
}

// metricsHandler returns JSON metrics for finished jobs.
func (service *ConverterService) metricsHandler(w http.ResponseWriter, r *http.Request) {
	service.mutex.RLock()
	metrics_data, err := json.Marshal(service.metrics)
	service.mutex.RUnlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(metrics_data)
}

// pollHandler allows clients to check job status or download the
// converted package.
func (service *ConverterService) pollHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobUUID, err := uuid.Parse(vars["uuid"])
	if err != nil {
		http.Error(w, fmt.Sprintf("uuid error:%+v %+v", vars, err), http.StatusBadRequest)
	}
	service.mutex.RLock()
	resp, ok := service.results[jobUUID]
	service.mutex.RUnlock()

	if r.Method == http.MethodHead {
		if ok {
			w.WriteHeader(http.StatusOK)
		} else {
			http.NotFound(w, r)
		}
	} else if r.Method == http.MethodGet {
		if ok {
			defer func() {
				service.mutex.RLock()
				delete(service.results, jobUUID)
				service.mutex.RUnlock()
			}()
			if resp == nil || resp.WorkingPackage == nil {
				sendError(w, fmt.Errorf("no working package for job uuid %s", jobUUID.String()))
				return
			}
			w.Header().Set("Content-Type", "application/zip")
			var buf bytes.Buffer
			err = service.converter.WriteDeploymentPackage(&buf, resp.WorkingPackage)
			if err != nil {
				sendError(w, err)
			} else {
				if !resp.Completed {
					w.WriteHeader(http.StatusNotAcceptable)
				} else {
					w.WriteHeader(http.StatusOK)
				}
				_, _ = w.Write(buf.Bytes())

			}
		} else {
			http.NotFound(w, r)
		}
	} else {
		http.Error(w, fmt.Sprintf("Unsupported method: %s", r.Method), http.StatusMethodNotAllowed)
	}
}

// sendError writes a JSON error response to the client.
func sendError(w http.ResponseWriter, core_err error) {

	errorMsg := make(map[string]string)
	errorMsg["error"] = core_err.Error()
	errorMsgDat, err := json.Marshal(errorMsg)
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(errorMsgDat)
	} else {
		http.Error(w, core_err.Error(), http.StatusInternalServerError)
	}
}

// uploadHandler accepts a multipart form with a `.zip` file and
// enqueues it for conversion.
func (service *ConverterService) uploadHandler(w http.ResponseWriter, r *http.Request) {
	// Limit file size (50MB max)
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20) // 50MB

	// Parse multipart form
	err := r.ParseMultipartForm(10 << 20) // 10MB buffer
	if err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	// Retrieve the file from the form-data
	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File not found in request", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Ensure it's a zip file
	if len(fileHeader.Filename) < 4 || fileHeader.Filename[len(fileHeader.Filename)-4:] != ".zip" {
		http.Error(w, "Only .zip files are allowed", http.StatusUnsupportedMediaType)
		return
	}

	// Read the file content into memory
	fileData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	// Process the file in memory using ReadDeploymentPackageFromReader
	dp, err := service.converter.ReadDeploymentPackageFromReader(io.NewSectionReader(
		&inMemoryReader{data: fileData}, 0, int64(len(fileData))),
		int64(len(fileData)),
	)
	if err != nil || dp == nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
	}

	request := MakeConversionRequest(dp)

	service.requestQueue <- request
	log.Infof("got new conversion request for %s", request.Id)
	http.Redirect(w, r, fmt.Sprintf("/%s", request.Id.String()), http.StatusCreated)
}

// inMemoryReader implements `io.ReaderAt` for an in-memory byte slice
// used by the upload handler.
type inMemoryReader struct {
	data []byte
}

// ReadAt reads data into p starting at offset off
func (r *inMemoryReader) ReadAt(p []byte, off int64) (n int, err error) {
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n = copy(p, r.data[off:])
	return n, nil
}

func (service *ConverterService) reconfigure(w http.ResponseWriter, r *http.Request) {
	var options ConverterOptions

	if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
		sendError(w, fmt.Errorf("error decoding options: %v", err))
	}

	service.mutex.Lock()
	err := service.converter.Reconfigure(&options)
	service.metrics = make(map[uuid.UUID]Metrics)
	service.results = make(map[uuid.UUID]*ConversionRequest)
	service.mutex.Unlock()

	if err != nil {
		sendError(w, err)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}
