package refaas_test

import (
	"bytes"
	"fmt"
	"iter"
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/inputhandler"
	"github.com/carl-egge/ise-predictive-refaas/internal/llmconnector"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	"github.com/carl-egge/ise-predictive-refaas/internal/translator"
	"github.com/stretchr/testify/assert"
)

func TestGemini(t *testing.T) {
	srcDep, err := inputhandler.ReadFromFile("test/f5.zip")
	assert.NoError(t, err)
	assert.NotNil(t, srcDep)

	req := pipeline.MakeConversionRequest(srcDep)

	promptBytes, err := os.ReadFile("internal/translator/prompts/stage-one.md")
	assert.NoError(t, err)
	promptTemplate, err := template.New("prompt").Parse(string(promptBytes))
	assert.NoError(t, err)

	var codePrompt bytes.Buffer

	codeBlock := codeBlockGenerator(req.SourcePackage)
	result := getFirstTestFile(req)
	err = promptTemplate.Execute(&codePrompt, map[string]interface{}{
		"code":     codeBlock.String(),
		"issue":    "",
		"original": "",
		"input":    result.Input,
		"output":   result.Output,
	})
	assert.NoError(t, err)

	gic := &llmconnector.GeminiInvocationClient{}

	gic.Configure(map[string]interface{}{
		"GEMINI_API_KEY": "AIzaSyBA5DW-Dzo01DGlLwp1J0AbLQ2R-10ThY4",
		"GEMINI_MODEL":   "gemini-1.5-flash-8b",
	})

	gic.Prepare(nil)

	response, metrics, err := gic.InvokeLLM(t.Context(), codePrompt)
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.NotNil(t, metrics)

	reader := translator.GoJsonOllamaReader{}
	out, err := reader.MakeDeploymentFile(response, req.SourcePackage)
	if err != nil {
		t.Errorf("error making deployment file: %v", err)
	}

	assert.NotEmpty(t, out)
	t.Logf("output: %s", out)
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
