package translator

import (
	_ "embed"

	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
)

//go:embed prompts/0-stage-document.md
var defaultCleanupPrompt string

//go:embed prompts/0-stage-summarize.md
var defaultSummaryPrompt string

//go:embed prompts/1-stage-translate.md
var defaultTranslatePrompt string

//go:embed prompts/2-stage-repair.md
var defaultRepairPrompt string

//go:embed prompts/3-stage-align.md
var defaultAlignmentPrompt string

func init() {
	// pipeline.RegisterConverterFactory("llmTask", NewLLMConverter)
	pipeline.RegisterConverterFactory("cleaner", NewCleanupConverter)
	pipeline.RegisterConverterFactory("summarizer", NewSummaryConverter)
	pipeline.RegisterConverterFactory("coder", NewTranslateConverter)
	pipeline.RegisterConverterFactory("fixer", NewRepairConverter)
	pipeline.RegisterConverterFactory("realign", NewAlignmentConverter)
}

// NewCleanupConverter configures a converter using the cleanup prompt.
func NewCleanupConverter(args map[string]interface{}) pipeline.Converter {
	return NewLLMConverter(args, defaultCleanupPrompt)
}

// NewSummaryConverter configures a converter using the summary prompt.
func NewSummaryConverter(args map[string]interface{}) pipeline.Converter {
	return NewLLMConverter(args, defaultSummaryPrompt)
}

// NewTranslateConverter configures a converter using the default translation prompt.
func NewTranslateConverter(args map[string]interface{}) pipeline.Converter {
	return NewLLMConverter(args, defaultTranslatePrompt)
}

// NewRepairConverter configures a converter using the repair prompt.
func NewRepairConverter(args map[string]interface{}) pipeline.Converter {
	return NewLLMConverter(args, defaultRepairPrompt)
}

// NewAlignmentConverter configures a converter using the alignment prompt.
func NewAlignmentConverter(args map[string]interface{}) pipeline.Converter {
	return NewLLMConverter(args, defaultAlignmentPrompt)
}
