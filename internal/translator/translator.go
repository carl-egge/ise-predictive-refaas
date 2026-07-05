package translator

import (
	"bytes"
	"fmt"
	"iter"
	"maps"
	"strings"
	"text/template"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/llmconnector"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	log "github.com/sirupsen/logrus"
)

// PackageReader converts a raw LLM string response into a DeploymentPackage
// representation.
type PackageReader interface {
	MakeDeploymentFile(rawLLMResponse string, original *domain.DeploymentPackage) (*domain.DeploymentPackage, error)
}

// LLMConverter renders a prompt template, invokes the configured LLM client and
// uses a reader to transform the response into a DeploymentPackage.
type LLMConverter struct {
	template *template.Template
	reader   PackageReader
	// mode is "package" (default - replace WorkingPackage with the parsed
	// response) or "metadata" (store the response in ConversionRequest.
	// Metadata instead, leaving WorkingPackage untouched).
	mode string
	// taskParams is this task's merged params (pipeline-wide options plus
	// this task's own task_args, minus "prompt"/"reader"/"mode"). It is
	// handed to the LLM client's Prepare on every Apply call — distinct
	// from pipeline.ConverterOptions.Args, which configures the connector
	// itself.
	taskParams map[string]interface{}
}

// TaskParams returns a copy of this task's merged params.
func (cc *LLMConverter) TaskParams() map[string]interface{} {
	out := make(map[string]interface{})
	maps.Copy(out, cc.taskParams)
	return out
}

// ReaderFactory returns a concrete PackageReader by name.
func ReaderFactory(name string) PackageReader {
	switch name {
	case "go":
		return GoJsonOllamaReader{}
	}
	return BasicLLMDeploymentReader{}
}

// failingConverter is returned by NewLLMConverter when the task configuration
// is invalid. Converter factories cannot return errors, and killing the whole
// process (log.Fatal) for one bad task config would take the service down
// during /reconfigure - so the error surfaces when the task runs instead.
type failingConverter struct{ err error }

func (fc *failingConverter) Apply(*pipeline.Runner, *domain.ConversionRequest) error {
	return fc.err
}

// NewLLMConverter builds an LLM-backed Converter from a task's merged params
// (pipeline-wide options plus that task's own task_args).
func NewLLMConverter(taskParams map[string]interface{}) pipeline.Converter {
	prompt, ok := taskParams["prompt"].(string)
	if !ok {
		err := fmt.Errorf("invalid LLM task configuration: prompt must be a string")
		log.Error(err)
		return &failingConverter{err: err}
	}

	promptTmpl, err := template.New("prompt").Parse(prompt)
	if err != nil {
		err = fmt.Errorf("invalid LLM task configuration: failed to parse prompt template: %w", err)
		log.Error(err)
		return &failingConverter{err: err}
	}

	var reader PackageReader
	if readerName, ok := taskParams["reader"].(string); ok {
		reader = ReaderFactory(readerName)
	} else {
		reader = BasicLLMDeploymentReader{}
	}

	mode, _ := taskParams["mode"].(string)
	if mode == "" {
		mode = "package"
	}

	delete(taskParams, "prompt")
	delete(taskParams, "reader")
	delete(taskParams, "mode")

	log.Debugf("creating LLM converter with params: %v", taskParams)
	return &LLMConverter{
		template:   promptTmpl,
		reader:     reader,
		mode:       mode,
		taskParams: taskParams,
	}
}

// Apply renders the prompt, calls the runner LLM client and updates the supplied
// ConversionRequest with the resulting DeploymentPackage.
func (cc *LLMConverter) Apply(runner *pipeline.Runner, code *domain.ConversionRequest) error {
	var codePrompt bytes.Buffer

	codeBlock := codeBlockGenerator(code.WorkingPackage)
	result := getFirstTestFile(code)

	srcFile := ""
	if code.SourcePackage != nil {
		srcFile = code.SourcePackage.RootFile
	}

	errStr := ""
	if last := code.LastError(); last != nil {
		errStr = last.Error()
	}

	// Known metadata keys (e.g. "intent" from a prior summary stage) are
	// promoted to top-level template vars so later prompts can reference
	// them directly, e.g. {{ .intent }}. The fixed vars below always take
	// precedence over a same-named metadata key.
	templateVars := make(map[string]interface{}, len(code.Metadata)+5)
	for k, v := range code.Metadata {
		templateVars[k] = v
	}
	templateVars["code"] = codeBlock.String()
	templateVars["issue"] = errStr
	templateVars["original"] = srcFile
	templateVars["input"] = result.Input
	templateVars["output"] = result.Output

	err := cc.template.Execute(&codePrompt, templateVars)
	if err != nil {
		code.AddError(err)
		return err
	}

	client := runner.LLMClient()
	if err := client.Prepare(cc.taskParams); err != nil {
		return domain.NewLLMError(fmt.Errorf("failed to configure LLMClient: %+v", err))
	}

	response, metrics, err := client.InvokeLLM(runner, codePrompt)
	if code.Metrics != nil {
		code.Metrics.AddMetric(metrics)
	}
	if err != nil {
		// Wrap as LLMError so executeTask can tell infrastructure failures
		// (API outage, rate limit, truncation) from code defects and skip
		// recovery prompts that cannot fix them.
		return domain.NewLLMError(fmt.Errorf("LLM invocation failed: %w", err))
	}

	// client.LogResponse(srcFile, response, codePrompt.String())
	// model_name is not set for every backend (Gemini uses GEMINI_MODEL), so
	// don't panic the task over a missing chatlog label.
	modelName, _ := cc.taskParams["model_name"].(string)
	if modelName == "" {
		modelName = "unknown-model"
	}
	llmconnector.LogResponse(modelName, codePrompt.String(), response)

	if cc.mode == "metadata" {
		return cc.applyMetadata(response, code)
	}

	original := code.WorkingPackage
	if original == nil {
		original = code.SourcePackage
	}
	newPackage, err := cc.reader.MakeDeploymentFile(response, original)
	code.WorkingPackage = newPackage

	if err != nil {
		code.AddError(domain.NewLLMError(err))
		return err
	}

	return nil
}

// applyMetadata parses response as a flat JSON object and merges its values
// into code.Metadata, leaving WorkingPackage untouched - used by tasks whose
// output (e.g. a summary's "intent") isn't a code artifact and shouldn't
// replace the package being translated.
func (cc *LLMConverter) applyMetadata(response string, code *domain.ConversionRequest) error {
	values, err := JsonCodeBlockReader(response)
	if err != nil {
		err = fmt.Errorf("metadata response could not be parsed as a flat JSON object: %w", err)
		code.AddError(domain.NewLLMError(err))
		return err
	}
	if len(values) == 0 {
		err := fmt.Errorf("metadata response contained no usable string fields: %.200s", response)
		code.AddError(domain.NewLLMError(err))
		return err
	}
	if code.Metadata == nil {
		code.Metadata = make(map[string]string)
	}
	maps.Copy(code.Metadata, values)
	return nil
}

func getFirstTestFile(code *domain.ConversionRequest) *domain.TestFile {
	next, stop := iter.Pull2(code.SourcePackage.GetTestFiles())
	result, err, valid := next()
	stop()
	if err != nil || !valid {
		result = &domain.TestFile{}
	}
	return result
}

// testHandlerFilename is the build-file name internal/builder.GolangBuilder
// injects for its fixed test harness (see builder.go's
// code.BuildFiles["test_handler.go"] = ...). It's never LLM-generated, so
// codeBlockGenerator excludes it from {{ .code }} - showing it to a
// translation/repair/alignment prompt only adds noise the model can't act
// on; any signature mismatch against it already surfaces in the build
// error text itself ({{ .issue }}).
const testHandlerFilename = "test_handler.go"

func codeBlockGenerator(code *domain.DeploymentPackage) strings.Builder {
	var codeBlock strings.Builder
	if code == nil {
		return codeBlock
	}
	codeBlock.WriteString(fmt.Sprintf("#### main.%s\n```%s\n", code.Suffix, fenceLanguage(code.Suffix)))
	codeBlock.WriteString(code.RootFile)
	codeBlock.WriteString("\n```\n\n")
	for fname, content := range code.BuildFiles {
		if fname == testHandlerFilename {
			continue
		}
		codeBlock.WriteString(fmt.Sprintf("\n#### %s\n```go\n", fname))
		codeBlock.WriteString(content)
		codeBlock.WriteString("\n```\n\n")
	}
	return codeBlock
}

// fenceLanguage maps a DeploymentPackage's Suffix to a Markdown code-fence
// language tag for the root file, so a pre-translation Python source isn't
// mislabeled as Go. BuildFiles are always Go artifacts in this codebase
// (go.mod, the injected test harness), so their own fence stays "go".
func fenceLanguage(suffix string) string {
	if suffix == "py" {
		return "python"
	}
	return "go"
}
