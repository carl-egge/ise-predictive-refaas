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
var defaultPrompt string

//go:embed prompts/1-stage-translate-2.md
var defaultPromptV2 string

//go:embed prompts/2-stage-repair.md
var defaultBuildRePrompt string

//go:embed prompts/3-stage-align.md
var defaultAlignmentPrompt string

func init() {
	pipeline.RegisterConverterFactory("llmTask", NewLLMConverter)
	pipeline.RegisterConverterFactory("cleaner", NewCleanupConverter)
	pipeline.RegisterConverterFactory("summary", NewSummaryConverter)
	pipeline.RegisterConverterFactory("coder", NewCodeConverter)
	pipeline.RegisterConverterFactory("coder2", NewCodeConverterV2)
	pipeline.RegisterConverterFactory("fixer", NewRePromptConverter)
	pipeline.RegisterConverterFactory("realign", NewAlignmentConverter)
}

// NewCleanupConverter configures a converter using the cleanup prompt.
func NewCleanupConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultCleanupPrompt
	return NewLLMConverter(taskParams)
}

// NewSummaryConverter configures a converter using the intent-summary
// prompt. Defaults to metadata mode (storing its output on
// ConversionRequest.Metadata rather than replacing WorkingPackage) with a
// single "intent" output key, both overridable via task_args.
func NewSummaryConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultSummaryPrompt
	if _, ok := taskParams["mode"]; !ok {
		taskParams["mode"] = "metadata"
	}
	if _, ok := taskParams["output_keys"]; !ok {
		taskParams["output_keys"] = map[string]interface{}{
			"intent": map[string]interface{}{
				"description": "One-sentence summary of the function's purpose, inputs, outputs, and key behavior",
			},
		}
	}
	return NewLLMConverter(taskParams)
}

// NewCodeConverter configures a converter using the default translation prompt.
func NewCodeConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultPrompt
	return NewLLMConverter(taskParams)
}

// NewCodeConverterV2 configures a converter using the intent-aware
// translation prompt (1-stage-translate-2.md), which expects {{ .intent }}
// to already be populated in ConversionRequest.Metadata - i.e. it should
// run after a "summary" task in the pipeline, not standalone.
func NewCodeConverterV2(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultPromptV2
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
