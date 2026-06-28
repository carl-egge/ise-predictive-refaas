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
func NewCleanupConverter(args map[string]interface{}) pipeline.Converter {
	args["prompt"] = defaultCleanupPrompt
	return NewLLMConverter(args)
}

// NewCodeConverter configures a converter using the default translation prompt.
func NewCodeConverter(args map[string]interface{}) pipeline.Converter {
	args["prompt"] = defaultPrompt
	return NewLLMConverter(args)
}

// NewRePromptConverter configures a converter using the build-fix prompt.
func NewRePromptConverter(args map[string]interface{}) pipeline.Converter {
	args["prompt"] = defaultBuildRePrompt
	return NewLLMConverter(args)
}

// NewAlignmentConverter configures a converter using the alignment prompt.
func NewAlignmentConverter(args map[string]interface{}) pipeline.Converter {
	args["prompt"] = defaultAlignmentPrompt
	return NewLLMConverter(args)
}
