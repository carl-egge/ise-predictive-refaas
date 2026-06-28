package translator

import (
	_ "embed"

	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
)

//go:embed prompts/0-stage-document.md
var defaultCleanupPrompt string

//go:embed prompts/1-stage-translate.md
var defaultPrompt string

//go:embed prompts/2-stage-repair.md
var defaultBuildRePrompt string

//go:embed prompts/3-stage-align.md
var defaultAlignmentPrompt string

func init() {
	pipeline.RegisterConverterFactory("llmTask", NewLLMConverter)
	pipeline.RegisterConverterFactory("cleaner", NewCleanupConverter)
	pipeline.RegisterConverterFactory("coder", NewCodeConverter)
	pipeline.RegisterConverterFactory("fixer", NewRePromptConverter)
	pipeline.RegisterConverterFactory("realign", NewAlignmentConverter)
}

// NewCleanupConverter configures a converter using the cleanup prompt.
func NewCleanupConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultCleanupPrompt
	return NewLLMConverter(taskParams)
}

// NewCodeConverter configures a converter using the default translation prompt.
func NewCodeConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultPrompt
	return NewLLMConverter(taskParams)
}

// NewRePromptConverter configures a converter using the build-fix prompt.
func NewRePromptConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultBuildRePrompt
	return NewLLMConverter(taskParams)
}

// NewAlignmentConverter configures a converter using the alignment prompt.
func NewAlignmentConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultAlignmentPrompt
	return NewLLMConverter(taskParams)
}
