package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/inputhandler"
	"github.com/carl-egge/ise-predictive-refaas/internal/llmconnector"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

// Runner ties together an LLM client, compiled Pipeline, and runtime context
// for performing conversions.
type Runner struct {
	context.Context
	client     llmconnector.Client
	pipeline   *Pipeline
	workingDir string
}

// NewRunner returns a Runner with the provided context, pipeline, and LLM client.
func NewRunner(ctx context.Context, pipe *Pipeline, client llmconnector.Client) *Runner {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Runner{
		Context:  ctx,
		pipeline: pipe,
		client:   client,
	}
}

// LLMClient returns the LLM connector for this runner.
func (cc *Runner) LLMClient() llmconnector.Client {
	return cc.client
}

// WorkingDir returns the current working directory used for builds/tests.
func (cc *Runner) WorkingDir() string {
	return cc.workingDir
}

// SetWorkingDir sets the working directory used for builds/tests.
func (cc *Runner) SetWorkingDir(dir string) {
	cc.workingDir = dir
}

// ConverterOptions holds configuration used when creating or reconfiguring a Runner.
type ConverterOptions struct {
	LLMClient string `json:"LLMClient"`
	// Args is connector-level config (API keys, endpoints), consumed once by
	// llmconnector.Client.Configure when the Runner is built/reconfigured.
	// It is merged with environment-derived defaults by setDefaults (see
	// envDefaults in defaults.go) and is distinct from the per-task params
	// in PipelineFile.Options/ConversionTaskStub.TaskArgs below, which are
	// re-evaluated on every task execution via Client.Prepare.
	Args map[string]any `json:"args"`

	// PipelineFile is embedded (rather than nested under a "pipeline" key) so
	// its Options/Tasks fields are promoted directly onto the JSON/YAML
	// representation of ConverterOptions.
	PipelineFile `yaml:",inline"`

	// Floci configures the optional Floci-backed integration testing backend.
	// When Floci.Enabled is true (and a Floci starter has been registered via
	// RegisterFlociStarter, i.e. the internal/floci package is linked in), the
	// backend is started/verified when the Runner is built or reconfigured.
	// When false, the "flociTester" stage — if present in the pipeline — is a
	// no-op, so the feature is fully opt-in.
	Floci FlociConfig `json:"floci,omitempty" yaml:"floci,omitempty"`

	CompiledPipeline *Pipeline `json:"compiledPipeline,omitempty"`
}

// FlociConfig holds the configuration for the optional Floci integration. It
// lives in the pipeline package (kept free of any AWS/Floci imports) so it can
// be carried on ConverterOptions and handed to a registered starter without
// creating an import cycle with internal/floci.
type FlociConfig struct {
	Enabled  bool   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Region   string `json:"region,omitempty" yaml:"region,omitempty"`
}

// flociStarter, if registered, is invoked with the resolved FlociConfig
// whenever a Runner is created or reconfigured with Floci.Enabled set. It is a
// package-level hook (mirroring converterFactories) so internal/floci can wire
// in its AWS-backed startup without the pipeline package importing it.
var flociStarter func(FlociConfig) error

// RegisterFlociStarter registers the Floci backend starter. internal/floci
// calls this from an init() so that merely importing it enables the feature.
func RegisterFlociStarter(fn func(FlociConfig) error) { flociStarter = fn }

// startFloci invokes the registered Floci starter when enabled. A startup
// failure (e.g. emulator unreachable) is logged as a warning rather than
// aborting Runner construction: the optional stage will surface a hard error at
// execution time, and we never want an unreachable emulator to take down the
// whole service.
func (co *ConverterOptions) startFloci() {
	if !co.Floci.Enabled || flociStarter == nil {
		return
	}
	if err := flociStarter(co.Floci); err != nil {
		log.Warnf("floci backend not started: %v", err)
	}
}

// setDefaults fills in a missing LLMClient and merges environment-derived
// defaults (see envDefaults) into Args, without overriding any value the
// caller already set explicitly.
func (co *ConverterOptions) setDefaults() {
	if co.LLMClient == "" {
		co.LLMClient = DefaultOptions.LLMClient
	}
	if co.Args == nil {
		co.Args = make(map[string]any)
	}
	for k, v := range envDefaults() {
		if _, ok := co.Args[k]; !ok {
			co.Args[k] = v
		}
	}
	co.Floci.applyEnvDefaults()
}

// applyEnvDefaults fills the Floci config from environment variables when the
// caller did not set values explicitly: FLOCI_ENABLED (true/1 enables it),
// FLOCI_ENDPOINT, and FLOCI_REGION. This lets docker-compose flip the feature
// on without a /reconfigure call.
func (fc *FlociConfig) applyEnvDefaults() {
	if !fc.Enabled {
		if v := os.Getenv("FLOCI_ENABLED"); v == "true" || v == "1" {
			fc.Enabled = true
		}
	}
	if fc.Endpoint == "" {
		fc.Endpoint = setOrDefault("FLOCI_ENDPOINT", "http://localhost:4566")
	}
	if fc.Region == "" {
		fc.Region = setOrDefault("FLOCI_REGION", "us-east-1")
	}
}

// DefaultOptions provides default converter configuration.
var DefaultOptions = ConverterOptions{
	LLMClient: "ollama",
}

// MakeCodeConverter constructs a Runner from ConverterOptions.
func MakeCodeConverter(ops *ConverterOptions) (*Runner, error) {
	if ops == nil {
		ops = &ConverterOptions{}
	}
	ops.setDefaults()

	factory, ok := llmconnector.Factories[ops.LLMClient]
	if !ok {
		return nil, fmt.Errorf("no LLM client factory found for %s", ops.LLMClient)
	}
	apiClient, err := factory(ops.Args)
	if err != nil {
		return nil, err
	}
	var pipeline *Pipeline
	if ops.CompiledPipeline != nil {
		pipeline = ops.CompiledPipeline
	} else if len(ops.Tasks) > 0 {
		pipeline, err = compilePipeline(ops.PipelineFile)
		if err != nil {
			return nil, err
		}
	}
	if pipeline == nil {
		pipeline, err = PipelineReader(bytes.NewReader([]byte(DefaultPipelineYAML)))
		if err != nil {
			return nil, err
		}
	}

	ops.startFloci()

	return &Runner{
		Context:  context.Background(),
		pipeline: pipeline,
		client:   apiClient,
	}, nil
}

// MakeConversionRequest creates a new ConversionRequest from a source package.
func MakeConversionRequest(srcPkg *domain.DeploymentPackage) *domain.ConversionRequest {
	return &domain.ConversionRequest{
		Id:            uuid.New(),
		SourcePackage: srcPkg,
		Metadata:      make(map[string]string),
		Metrics: &domain.Metrics{
			TestCases: make(map[string]bool),
		},
	}
}

// Convert runs the configured pipeline against req using ctx as the
// cancellation source for this conversion: cancelling ctx aborts the
// pipeline at the next opportunity (between retries/tasks) instead of
// continuing to spend build/test/LLM resources on it.
func (cc *Runner) Convert(ctx context.Context, req *domain.ConversionRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cc.Context = ctx
	req.WorkingPackage = req.SourcePackage.Copy()
	return cc.pipeline.Execute(cc, req)
}

// Reconfigure updates the runner with new ConverterOptions, swapping its pipeline and LLM client.
func (cc *Runner) Reconfigure(ops *ConverterOptions) error {
	ops.setDefaults()
	factory, ok := llmconnector.Factories[ops.LLMClient]
	if !ok {
		return fmt.Errorf("no LLM client factory found for %s", ops.LLMClient)
	}
	apiClient, err := factory(ops.Args)
	if err != nil {
		return err
	}

	ops.startFloci()

	if len(ops.Tasks) == 0 && ops.CompiledPipeline == nil {
		return fmt.Errorf("no pipeline specified")
	}

	if ops.CompiledPipeline != nil {
		cc.workingDir = ""
		cc.pipeline = ops.CompiledPipeline
		cc.client = apiClient
		return nil
	}

	pipeline, err := compilePipeline(ops.PipelineFile)
	if err != nil {
		return err
	}

	if cc.workingDir != "" {
		defer os.RemoveAll(cc.workingDir)
	}

	cc.workingDir = ""
	cc.pipeline = pipeline
	cc.client = apiClient

	return nil
}

// ConvertFromFileBest reads a deployment package from sourceFile, marks it as a
// Python source and runs a conversion, returning the request and any error.
func (cc *Runner) ConvertFromFileBest(sourceFile string) (*domain.ConversionRequest, error) {
	dp, err := inputhandler.ReadFromFile(sourceFile)
	if err != nil {
		return nil, err
	}
	dp.Suffix = "py"
	log.Debugf("got deployment package: %s - %+v", sourceFile, dp)

	req := MakeConversionRequest(dp)
	if err := cc.Convert(context.Background(), req); err != nil {
		return nil, err
	}

	return req, nil
}
