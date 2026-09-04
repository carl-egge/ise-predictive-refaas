package translator

import (
	"strings"
	"testing"
	"text/template"
)

// embeddedPrompts is every prompt the shipped converters use. Keep it in sync
// with the //go:embed block in prompts.go.
func embeddedPrompts() map[string]string {
	return map[string]string{
		"0-stage-document.md":    defaultCleanupPrompt,
		"0-stage-summarize.md":   defaultSummaryPrompt,
		"1-stage-translate-1.md": defaultPrompt,
		"1-stage-translate-2.md": defaultPromptV2,
		"2-stage-repair.md":      defaultBuildRePrompt,
		"3-stage-align.md":       defaultAlignmentPrompt,
	}
}

// TestEmbeddedPromptsParseAndRender guards the prompts as *code*. They are
// text/template sources edited by hand, and a mismatched {{ if }} / {{ end }}
// surfaces only when a converter is constructed - which, for a stage reached
// after a failure, can be an hour into a benchmark run.
func TestEmbeddedPromptsParseAndRender(t *testing.T) {
	// A superset of what LLMConverter.Apply supplies: the fixed vars plus the
	// metadata keys pyScan publishes, which Apply promotes to top level.
	vars := map[string]any{
		"code": "package main", "issue": "boom", "original": "def f(): pass",
		"input": "{}", "output": "{}", "tests": "", "failures": "",
		"intent": "does a thing", "stagnant": "",
		"lib_hints": "- boto3 -> ...", "aws_hints": "- aws.Bool ...",
		"py_features": "- 1 comprehension", "feasibility_warning": "",
		"py_cc": "4", "py_lloc": "20",
	}

	for name, src := range embeddedPrompts() {
		t.Run(name, func(t *testing.T) {
			// Option("missingkey=error") is deliberately NOT set: Apply renders
			// with whatever vars exist, and a prompt referencing an absent key
			// must degrade to empty rather than fail a conversion.
			tmpl, err := template.New(name).Parse(src)
			if err != nil {
				t.Fatalf("prompt does not parse: %v", err)
			}
			var full, empty strings.Builder
			if err := tmpl.Execute(&full, vars); err != nil {
				t.Fatalf("render with all vars: %v", err)
			}
			// The conditional blocks must also survive the non-AWS case, which
			// is 37 of the corpus's 95 functions.
			if err := tmpl.Execute(&empty, map[string]any{}); err != nil {
				t.Fatalf("render with no vars: %v", err)
			}
			if strings.Contains(empty.String(), "AWS SDK for Go v2") {
				t.Error("the AWS block rendered for a function with no aws_hints")
			}
			// The summary stage is optional, so {{ .intent }} can legitimately
			// be absent; an unguarded placeholder would leave the model a bare
			// "Intent:" heading with nothing under it.
			if strings.Contains(empty.String(), "Intent:") {
				t.Error("the intent heading rendered with no intent behind it")
			}
		})
	}
}

// TestAWSHintsReachEveryStageThatCanUseThem pins the wiring: the model writes
// AWS SDK v1 and boto3 shapes at translate time, the build fails on them, and
// the test round fails on the runtime half - so all three stages need the
// block. The fixer having no AWS guidance at all is why it spent 926k tokens
// in run 20260831-190900 without clearing 20 build failures.
func TestAWSHintsReachEveryStageThatCanUseThem(t *testing.T) {
	prompts := embeddedPrompts()
	for _, name := range []string{
		"1-stage-translate-1.md", "1-stage-translate-2.md",
		"2-stage-repair.md", "3-stage-align.md",
	} {
		if !strings.Contains(prompts[name], "{{ .aws_hints }}") {
			t.Errorf("%s does not render {{ .aws_hints }}", name)
		}
	}
	// The fixer is the stage that sees "finding module for package ..." and
	// needs the exact import paths to act on it.
	if !strings.Contains(prompts["2-stage-repair.md"], "{{ .lib_hints }}") {
		t.Error("2-stage-repair.md does not render {{ .lib_hints }}; the fixer cannot check an import path it was never given")
	}
}
