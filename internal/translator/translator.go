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
	args     map[string]interface{}
}

// Args returns a copy of the converter arguments.
func (cc *LLMConverter) Args() map[string]interface{} {
	out := make(map[string]interface{})
	maps.Copy(out, cc.args)
	return out
}

// ReaderFactory returns a concrete PackageReader by name.
func ReaderFactory(name string) PackageReader {
	switch name {
	case "go":
		return GoJsonOllamaReader{}
	case "deepseek":
		return GoDeepSeekOllamaReader{}
	}
	return BasicLLMDeploymentReader{}
}

// NewLLMConverter builds an LLM-backed Converter from template args.
func NewLLMConverter(args map[string]interface{}, prompt string) pipeline.Converter {
	if prompt == "" {
		log.Fatal("prompt is missing")
		return nil
	}

	promptTmpl, err := template.New("prompt").Parse(prompt)
	if err != nil {
		log.Fatalf("Failed to parse prompt template: %s", err)
		return nil
	}

	var reader PackageReader
	if readerName, ok := args["reader"].(string); ok {
		reader = ReaderFactory(readerName)
	} else {
		reader = BasicLLMDeploymentReader{}
	}

	delete(args, "reader")

	return &LLMConverter{
		template: promptTmpl,
		reader:   reader,
		args:     args,
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

	err := cc.template.Execute(&codePrompt, map[string]interface{}{
		"code":     codeBlock.String(),
		"issue":    errStr,
		"original": srcFile,
		"input":    result.Input,
		"output":   result.Output,
	})
	if err != nil {
		code.AddError(err)
		return err
	}

	client := runner.LLMClient()
	if err := client.Prepare(cc.args); err != nil {
		return domain.NewLLMError(fmt.Errorf("failed to configure LLMClient: %+v", err))
	}

	log.Debugf("invoking LLM (%s) with llm client (%s)", cc.args["model_name"], cc.args["llmClientName"])

	response, metrics, err := client.InvokeLLM(runner, codePrompt.String())
	if code.Metrics != nil {
		code.Metrics.AddMetric(metrics)
	}
	if err != nil {
		return err
	}

	// client.LogResponse(srcFile, response, codePrompt.String())
	llmconnector.LogResponse(cc.args["model_name"].(string), codePrompt.String(), response)
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

func getFirstTestFile(code *domain.ConversionRequest) *domain.TestFile {
	next, stop := iter.Pull2(code.SourcePackage.GetTestFiles())
	result, err, valid := next()
	stop()
	if err != nil || !valid {
		result = &domain.TestFile{}
	}
	return result
}

func codeBlockGenerator(code *domain.DeploymentPackage) strings.Builder {
	var codeBlock strings.Builder
	if code == nil {
		return codeBlock
	}
	// codeBlock.WriteString(fmt.Sprintf("#### main.%s\n```go\n", code.Suffix))
	codeBlock.WriteString(fmt.Sprintf("#### main.%s\n```\n", code.Suffix))
	codeBlock.WriteString(code.RootFile)
	codeBlock.WriteString("\n```\n\n")
	for _, fname := range code.BuildFiles {
		codeBlock.WriteString(fmt.Sprintf("\n#### %s\n```go\n", fname))
		codeBlock.WriteString(code.BuildFiles[fname])
		codeBlock.WriteString("\n```\n\n")
	}
	return codeBlock
}
