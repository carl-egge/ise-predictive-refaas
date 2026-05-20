package service

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

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/inputhandler"
	"github.com/carl-egge/ise-predictive-refaas/internal/outputhandler"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"
)

// ConverterService manages background conversion jobs and exposes an HTTP interface.
type ConverterService struct {
	converter    *pipeline.Runner
	requestQueue chan *domain.ConversionRequest
	results      map[uuid.UUID]*domain.ConversionRequest
	metrics      map[uuid.UUID]domain.Metrics
	mutex        sync.RWMutex
}

func setOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func setFileFromEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		fs, err := os.OpenFile(val, os.O_RDONLY, 0644)
		if err != nil {
			return defaultValue
		}
		defer fs.Close()
		fsdat, err := io.ReadAll(fs)
		if err != nil {
			return defaultValue
		}
		if len(fsdat) == 0 {
			return defaultValue
		}
		return string(fsdat)
	}
	return defaultValue
}

// MakeConverterService constructs and starts the HTTP converter service; it
// blocks by calling http.ListenAndServe.
func MakeConverterService() error {
	options := pipeline.DefaultOptions
	options.Args = maps.Clone(pipeline.DefaultOptions.Args)
	options.Args["OLLAMA_API_URL"] = setOrDefault("OLLAMA_API_URL", pipeline.DefaultOllamaAPIURL)
	options.Args["GEMINI_API_KEY"] = setOrDefault("GEMINI_API_KEY", "NOT+SET")

	converter, err := pipeline.MakeCodeConverter(&options)
	if err != nil {
		return err
	}

	sv := ConverterService{
		converter:    converter,
		requestQueue: make(chan *domain.ConversionRequest, 100),
		results:      make(map[uuid.UUID]*domain.ConversionRequest),
		metrics:      make(map[uuid.UUID]domain.Metrics),
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

// Start runs the background worker loop that processes queued conversion requests.
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
			service.mutex.RLock()
			delete(service.results, jobUUID)
			service.mutex.RUnlock()
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
	if err != nil || dp == nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	request := pipeline.MakeConversionRequest(dp)

	service.requestQueue <- request
	log.Infof("got new conversion request for %s", request.Id)
	http.Redirect(w, r, fmt.Sprintf("/%s", request.Id.String()), http.StatusCreated)
}

func (service *ConverterService) reconfigure(w http.ResponseWriter, r *http.Request) {
	var options pipeline.ConverterOptions

	if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
		outputhandler.WriteHTTPError(w, fmt.Errorf("error decoding options: %v", err))
		return
	}

	service.mutex.Lock()
	err := service.converter.Reconfigure(&options)
	service.metrics = make(map[uuid.UUID]domain.Metrics)
	service.results = make(map[uuid.UUID]*domain.ConversionRequest)
	service.mutex.Unlock()

	if err != nil {
		outputhandler.WriteHTTPError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}
