package pipeline

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/inputhandler"
	"github.com/carl-egge/ise-predictive-refaas/internal/llmconnector"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	_ "github.com/joho/godotenv/autoload"
)

// Runner ties together an LLM client, compiled Pipeline, and runtime context
// for performing conversions. It can also store a working directory and
// runtime arguments for the LLM client and Floci.
type Runner struct {
	context.Context
	client     llmconnector.Client
	pipeline   *Pipeline
	workingDir string
	args       map[string]interface{}
	flociArgs  map[string]interface{}
}

// NewRunner returns a Runner with the provided context, pipeline, and LLM client.
func NewRunner(ctx context.Context, pipe *Pipeline, client llmconnector.Client) *Runner {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Runner{
		Context:   ctx,
		pipeline:  pipe,
		client:    client,
		args:      make(map[string]interface{}),
		flociArgs: make(map[string]interface{}),
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

	LLMClient string         `json:"llmClient"` // Name of the LLM client to use (e.g., "ollama", "gemini", "chatai")
	Args      map[string]any `json:"args"`      // Pipeline options + environment variables for the LLM client
	FlociArgs map[string]any `json:"flociArgs"` // Optional arguments for Floci, if used in the pipeline
}

// DefaultPipelineYAML contains the embedded default pipeline configuration.
//
//go:embed default.yaml
var DefaultPipelineYAML string

// DefaultOptions provides default converter configuration.
var DefaultOptions = initDefaultOptions()

// MakeCodeConverter constructs a Runner from ConverterOptions.
func MakeCodeConverter() (*Runner, error) {

	// Read default options from default.yaml and environment variables
	convOps := DefaultOptions

	factory, ok := llmconnector.Factories[convOps.LLMClient]
	if !ok {
		return nil, fmt.Errorf("no LLM client factory found for %s", convOps.LLMClient)
	}
	apiClient, err := factory(convOps.Args)
	if err != nil {
		return nil, err
	}
	var pipeline *Pipeline
	if convOps.CompiledPipeline != nil {
		pipeline = convOps.CompiledPipeline
	} else if convOps.Pipeline != nil {
		pipeline, err = compilePipeline(*convOps.Pipeline)
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

	log.Infof("creating runner with %s:%s", apiClient.ClientName(), convOps.Args["model_name"])

	return &Runner{
		Context:   context.Background(),
		pipeline:  pipeline,
		client:    apiClient,
		args:      convOps.Args,
		flociArgs: convOps.FlociArgs,
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

// Reconfigure updates the runner with new ConverterOptions, using only the provided options
// and loading environment variables via setOrDefault() to construct Args.
func (cc *Runner) Reconfigure(ops *ConverterOptions) error {
	// Use the provided LLMClient, or fall back to default if not set
	llmClient := ops.LLMClient
	if llmClient == "" {
		llmClient = DefaultOptions.LLMClient
	}

	// Build Args from environment variables using setOrDefault
	args := map[string]any{
		"OLLAMA_API_URL":          setOrDefault("OLLAMA_API_URL", "http://localhost:11434"),
		"GEMINI_API_KEY":          setOrDefault("GEMINI_API_KEY", "NOT+SET"),
		"ACADEMIC_CLOUD_ENDPOINT": setOrDefault("ACADEMIC_CLOUD_ENDPOINT", "https://chat-ai.academiccloud.de/v1"),
		"ACADEMIC_CLOUD_API_KEY":  setOrDefault("ACADEMIC_CLOUD_API_KEY", "NOT+SET"),
		"APP_PORT":                setOrDefault("APP_PORT", "8080"),
	}

	// Merge any provided Args from ops (overrides env vars)
	if ops.Args != nil {
		for k, v := range ops.Args {
			args[k] = v
		}
	}

	// Build FlociArgs from provided FlociArgs or fallback to defaults
	flociArgs := make(map[string]any)
	if ops.FlociArgs != nil {
		for k, v := range ops.FlociArgs {
			flociArgs[k] = v
		}
	}

	// Get LLM client factory
	factory, ok := llmconnector.Factories[llmClient]
	if !ok {
		return fmt.Errorf("no LLM client factory found for %s", llmClient)
	}

	apiClient, err := factory(args)
	if err != nil {
		return err
	}

	// Validate pipeline
	if ops.Pipeline == nil && ops.CompiledPipeline == nil {
		return fmt.Errorf("no pipeline specified")
	}

	// Handle compiled pipeline
	if ops.CompiledPipeline != nil {
		cc.workingDir = ""
		cc.pipeline = ops.CompiledPipeline
		cc.client = apiClient
		cc.args = args
		cc.flociArgs = flociArgs
		return nil
	}

	// Compile new pipeline from ops.Pipeline
	pipeline, err := compilePipeline(*ops.Pipeline)
	if err != nil {
		return err
	}

	// Clean up old working dir if needed
	if cc.workingDir != "" {
		defer os.RemoveAll(cc.workingDir)
	}

	cc.workingDir = ""
	cc.pipeline = pipeline
	cc.client = apiClient
	cc.args = args
	cc.flociArgs = flociArgs

	log.Infof("creating runner with %s:%s", apiClient.ClientName(), cc.args["model_name"])

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

// initDefaultOptions initializes the DefaultOptions by parsing default.yaml
// and merging its content with environment variables.
func initDefaultOptions() ConverterOptions {
	var config struct {
		LLMClient    string                 `yaml:"LLMClient"`
		Options      map[string]interface{} `yaml:"options"`
		FlociOptions map[string]interface{} `yaml:"flociOptions"`
		Tasks        []interface{}          `yaml:"tasks"` // ignored for now
	}

	err := yaml.Unmarshal([]byte(DefaultPipelineYAML), &config)
	if err != nil {
		panic(fmt.Sprintf("failed to unmarshal default.yaml: %v", err))
	}

	// Start with environment variables
	args := map[string]any{
		"OLLAMA_API_URL":          setOrDefault("OLLAMA_API_URL", "http://localhost:11434"),
		"GEMINI_API_KEY":          setOrDefault("GEMINI_API_KEY", "NOT+SET"),
		"ACADEMIC_CLOUD_ENDPOINT": setOrDefault("ACADEMIC_CLOUD_ENDPOINT", "https://chat-ai.academiccloud.de/v1"),
		"ACADEMIC_CLOUD_API_KEY":  setOrDefault("ACADEMIC_CLOUD_API_KEY", "NOT+SET"),
		"APP_PORT":                setOrDefault("APP_PORT", "8080"),
	}

	// Merge options from default.yaml into Args
	if config.Options != nil {
		for k, v := range config.Options {
			args[k] = v
		}
	}

	// Set LLMClient from YAML (if present), otherwise default to "ollama"
	llmClient := config.LLMClient
	if llmClient == "" {
		llmClient = "ollama"
	}

	// Parse PipelineFile from default.yaml
	var pipelineFile PipelineFile
	if err := yaml.Unmarshal([]byte(DefaultPipelineYAML), &pipelineFile); err != nil {
		panic(fmt.Sprintf("failed to unmarshal PipelineFile from default.yaml: %v", err))
	}

	// Parse FlociArgs from flociOptions
	flociArgs := make(map[string]any)
	if config.FlociOptions != nil {
		for k, v := range config.FlociOptions {
			flociArgs[k] = v
		}
	}

	return ConverterOptions{
		LLMClient:        llmClient,
		Args:             args,
		FlociArgs:        flociArgs,
		Pipeline:         &pipelineFile,
		CompiledPipeline: nil,
	}
}

// Set environment variable or return default value if not set.
// Using the lazy autoloader (_ "github.com/joho/godotenv/autoload") gets the env vars loaded
// before this runs, so we can set defaults from env or hardcoded values.
func setOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
