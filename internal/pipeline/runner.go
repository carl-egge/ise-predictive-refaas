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
	Pipeline         *PipelineFile `json:"pipeline,omitempty"`
	CompiledPipeline *Pipeline     `json:"compiledPipeline,omitempty"`

	LLMClient string         `json:"LLMClient"`
	Args      map[string]any `json:"args"`
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
	} else if ops.Pipeline != nil {
		pipeline, err = compilePipeline(*ops.Pipeline)
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
		Metrics: &domain.Metrics{
			TestCases: make(map[string]bool),
		},
	}
}

// Convert runs the configured pipeline against req and returns any error encountered.
func (cc *Runner) Convert(req *domain.ConversionRequest) error {
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

	if ops.Pipeline == nil && ops.CompiledPipeline == nil {
		return fmt.Errorf("no pipeline specified")
	}

	if ops.CompiledPipeline != nil {
		cc.workingDir = ""
		cc.pipeline = ops.CompiledPipeline
		cc.client = apiClient
		return nil
	}

	pipeline, err := compilePipeline(*ops.Pipeline)
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
	if err := cc.Convert(req); err != nil {
		return nil, err
	}

	return req, nil
}
