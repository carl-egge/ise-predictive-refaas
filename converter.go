// Package main provides the converter orchestration and option parsing
// used to create and run `PipelineRunner` instances.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

// PipelineRunner ties together an `LLMInvocationClient`, compiled
// `Pipeline`, and runtime context for performing conversions.
type PipelineRunner struct {
	context.Context
	client LLMInvocationClient

	pipeline   *Pipeline
	WorkingDir string
}

// ConverterOptions holds configuration used when creating or
// reconfiguring a `PipelineRunner`.
type ConverterOptions struct {
	Pipeline         *PipelineFile `json:"pipeline",omitempty`
	CompiledPipeline *Pipeline     `json:"compiledPipeline,omitempty"`

	LLMClient string         `json:"LLMClient"`
	Args      map[string]any `json:"args"`
}

func (co *ConverterOptions) setDefaults() {
	if co.LLMClient == "" {
		co.LLMClient = DefaultOptions.LLMClient
	}
	if co.Args == nil {
		co.Args = DefaultOptions.Args
	} else {
		for k, v := range DefaultOptions.Args {
			if _, ok := co.Args[k]; !ok {
				co.Args[k] = v
			}
		}
	}
}

var DefaultOptions = ConverterOptions{
	LLMClient: "ollama",
	Args: map[string]any{
		"OLLAMA_API_URL": OLLAMA_API_URL,
	},
}

// MakeCodeConverter constructs a `PipelineRunner` from `ConverterOptions`.
func MakeCodeConverter(ops *ConverterOptions) (*PipelineRunner, error) {
	if ops == nil {
		ops = &DefaultOptions
	} else {
		ops.setDefaults()
	}

	//XXX placeholder
	api_client, err := LLMClientFactories[ops.LLMClient](ops.Args)
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

	return &PipelineRunner{
		Context:  context.Background(),
		pipeline: pipeline,
		client:   api_client,
	}, nil
}

// MakeConversionRequest creates a new `ConversionRequest` from a
// source `DeploymentPackage`.
func MakeConversionRequest(srcPkg *DeploymentPackage) *ConversionRequest {
	return &ConversionRequest{
		Id:            uuid.New(),
		SourcePackage: srcPkg,
		Metrics: &Metrics{
			TestCases: make(map[string]bool),
		},
		err: make([]error, 0),
	}
}

// Convert runs the configured pipeline against `req` and returns any
// error encountered.
func (cc *PipelineRunner) Convert(req *ConversionRequest) error {
	req.WorkingPackage = req.SourcePackage.copy()

	return cc.pipeline.Execute(cc, req)
}

// Reconfigure updates the runner with new `ConverterOptions`, swapping
// its pipeline and LLM client.
func (cc *PipelineRunner) Reconfigure(ops *ConverterOptions) error {
	ops.setDefaults()
	api_client, err := LLMClientFactories[ops.LLMClient](ops.Args)
	if err != nil {
		return err
	}

	if ops.Pipeline == nil {
		if ops.CompiledPipeline == nil {
			return fmt.Errorf("no pipeline specified")
		}
	}

	if ops.CompiledPipeline != nil {
		cc.WorkingDir = ""
		cc.pipeline = ops.CompiledPipeline
		cc.client = api_client
		return nil
	}

	pipeline, err := compilePipeline(*ops.Pipeline)
	if err != nil {
		return err
	}

	if cc.WorkingDir != "" {
		defer os.RemoveAll(cc.WorkingDir)
	}

	cc.WorkingDir = ""
	cc.pipeline = pipeline
	cc.client = api_client

	return nil
}

// ConvertFromFileBest reads a deployment package from `sourceFile`,
// marks it as a Python source and runs a conversion, returning the
// request and any error.
func (cc *PipelineRunner) ConvertFromFileBest(sourceFile string) (*ConversionRequest, error) {
	dp, err := cc.ReadDeploymentPackageFromFile(sourceFile)
	if err != nil {
		return nil, err
	}
	dp.Suffix = "py"
	log.Debugf("got deployment package: %s - %+v", sourceFile, dp)

	req := MakeConversionRequest(dp)
	err = cc.Convert(req)
	if err != nil {
		return nil, err
	}

	return req, nil
}
