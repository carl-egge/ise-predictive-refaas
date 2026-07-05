package translator

import (
	_ "embed"

	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
)

//go:embed prompts/0-stage-document.md
var defaultCleanupPrompt string

//go:embed prompts/0-stage-summarize.md
var defaultSummaryPrompt string

//go:embed prompts/1-stage-translate-1.md
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

// defaultOutputKeys returns a closed, single-file response schema for a
// code-producing stage: exactly one required (non-nullable) file key. A
// closed shape (no unbounded additionalProperties) is deliberately used
// because schema enforcement on the ChatAI backend is per-model and weaker
// models silently drop grammars they cannot compile - fixed shapes were the
// reliably enforced subset. Overridable per task via task_args.output_keys.
func defaultOutputKeys(filename, description string) map[string]interface{} {
	return map[string]interface{}{
		filename: map[string]interface{}{
			"nullable":    false,
			"description": description,
		},
	}
}

// setDefaultOutputKeys applies schema defaults only when the pipeline config
// didn't set its own output_keys (mirroring NewSummaryConverter's pattern).
func setDefaultOutputKeys(taskParams map[string]interface{}, filename, description string) {
	if _, ok := taskParams["output_keys"]; !ok {
		taskParams["output_keys"] = defaultOutputKeys(filename, description)
	}
}

// NewCleanupConverter configures a converter using the cleanup prompt.
func NewCleanupConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultCleanupPrompt
	setDefaultOutputKeys(taskParams, "main.py",
		"The complete, documented Python source of the function")
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

// goSourceDescription is the shared output_keys description for every stage
// whose response is the translated/repaired Go source. go.mod/go.sum are
// deliberately not part of any schema: the pipeline regenerates them
// deterministically (go mod init + go mod tidy) and the readers drop any the
// LLM returns anyway.
const goSourceDescription = "The complete Go source of the handler (package main, no main() function, all imports included)"

// NewCodeConverter configures a converter using the default translation prompt.
func NewCodeConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultPrompt
	setDefaultOutputKeys(taskParams, "main.go", goSourceDescription)
	return NewLLMConverter(taskParams)
}

// NewCodeConverterV2 configures a converter using the intent-aware
// translation prompt (1-stage-translate-2.md), which expects {{ .intent }}
// to already be populated in ConversionRequest.Metadata - i.e. it should
// run after a "summary" task in the pipeline, not standalone.
func NewCodeConverterV2(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultPromptV2
	setDefaultOutputKeys(taskParams, "main.go", goSourceDescription)
	return NewLLMConverter(taskParams)
}

// NewRePromptConverter configures a converter using the build-fix prompt.
func NewRePromptConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultBuildRePrompt
	setDefaultOutputKeys(taskParams, "main.go", goSourceDescription)
	return NewLLMConverter(taskParams)
}

// NewAlignmentConverter configures a converter using the alignment prompt.
func NewAlignmentConverter(taskParams map[string]interface{}) pipeline.Converter {
	taskParams["prompt"] = defaultAlignmentPrompt
	setDefaultOutputKeys(taskParams, "main.go", goSourceDescription)
	return NewLLMConverter(taskParams)
}
