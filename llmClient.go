// Package main contains the glue between prompts and LLM readers used to
// parse model responses into `DeploymentPackage` instances.
package main

import (
	"bytes"
	"fmt"
	"iter"
	"strings"
	"text/template"

	log "github.com/sirupsen/logrus"
)

// LLMPackageReader converts a raw LLM string response into a
// `DeploymentPackage` representation.
type LLMPackageReader interface {
	makeDeploymentFile(rawLLMResponse string, original *DeploymentPackage) (*DeploymentPackage, error)
}

// LLMConverter renders a prompt template, invokes the configured LLM
// client and uses a reader to transform the response into a
// `DeploymentPackage`.
type LLMConverter struct {
	template *template.Template
	reader   LLMPackageReader
	args     map[string]interface{}
}

// ReaderFactory returns a concrete `LLMPackageReader` by name.
func ReaderFactory(name string) LLMPackageReader {
	switch name {
	case "go":
		return GoJsonOllamaReader{}
	case "deepseek":
		return GoDeepSeekOllamaReader{}
	}
	return BasicLLMDeploymentReader{}
}

// makeLLMConverter builds an LLM-backed `Converter` from template args.
func makeLLMConverter(args map[string]interface{}) Converter {

	prompt, ok := args["prompt"].(string)
	if !ok {
		log.Fatal("prompt must be a string")
		return nil
	}

	prompt_tmpl, err := template.New("prompt").Parse(prompt)
	if err != nil {
		log.Fatalf("Failed to parse prompt template: %s", err)
		return nil
	}

	var reader LLMPackageReader
	if readerName, ok := args["reader"].(string); ok {
		reader = ReaderFactory(readerName)
	} else {
		reader = BasicLLMDeploymentReader{}
	}

	delete(args, "prompt")
	delete(args, "reader")

	log.Debugf("creating LLM converter with params: %v", args)
	return &LLMConverter{
		template: prompt_tmpl,
		reader:   reader,
		args:     args,
	}
}

// Apply renders the prompt, calls the runner LLM client and updates the
// supplied `ConversionRequest` with the resulting `DeploymentPackage`.
func (cc *LLMConverter) Apply(runner *PipelineRunner, code *ConversionRequest) error {
	var codePrompt bytes.Buffer

	codeBlock := codeBlockGenerator(code.WorkingPackage)
	result := getFirstTestFile(code)

	srcFile := ""
	if code.SourcePackage != nil {
		srcFile = code.SourcePackage.RootFile
	}
	errStr := ""
	if code.err != nil && len(code.err) > 0 {
		errStr = code.err[len(code.err)-1].Error()
	}

	err := cc.template.Execute(&codePrompt, map[string]interface{}{
		"code":     codeBlock.String(),
		"issue":    errStr,
		"original": srcFile,
		"input":    result.Input,
		"output":   result.Output,
	})
	if err != nil {
		code.err = append(code.err, err)
		return err
	}

	err = runner.client.Prepare(cc.args)
	if err != nil {
		return LLMError{fmt.Errorf("failed to configure LLMClient: %+v", err)}
	}
	response, metrics, err := runner.client.InvokeLLM(runner, codePrompt)
	code.Metrics.AddMetric(metrics)
	if err != nil {
		return err
	}

	runner.client.logLLMResponse(srcFile, response, codePrompt.String())
	original := code.WorkingPackage
	if original == nil {
		original = code.SourcePackage
	}
	newPackage, err := cc.reader.makeDeploymentFile(response, original)
	code.WorkingPackage = newPackage

	if err != nil {
		code.err = append(code.err, LLMError{err})
		return err
	}

	return nil
}

// getFirstTestFile returns the first `TestFile` from the source package
// or an empty `TestFile` when none are present.
func getFirstTestFile(code *ConversionRequest) *TestFile {
	next, stop := iter.Pull2(code.SourcePackage.getTestFiles())
	result, err, valid := next()
	stop()
	if err != nil || !valid {
		result = &TestFile{}
	}
	return result
}

// codeBlockGenerator builds a markdown code block representing the
// package files used in the prompt templates.
func codeBlockGenerator(code *DeploymentPackage) strings.Builder {
	var codeBlock strings.Builder
	if code == nil {
		return codeBlock
	}
	codeBlock.WriteString(fmt.Sprintf("#### main.%s\n```go\n", code.Suffix))
	codeBlock.WriteString(code.RootFile)
	codeBlock.WriteString("\n```\n\n")
	for _, fname := range code.BuildFiles {
		codeBlock.WriteString(fmt.Sprintf("\n#### %s\n```go\n", fname))
		codeBlock.WriteString(code.BuildFiles[fname])
		codeBlock.WriteString("\n```\n\n")
	}
	return codeBlock
}
