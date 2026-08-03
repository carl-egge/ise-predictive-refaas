package translator

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
	"github.com/carl-egge/ise-predictive-refaas/internal/llmconnector"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	"github.com/google/uuid"
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
	// maxExamples caps how many test cases {{ .tests }} renders
	// (task_args "max_test_examples", default defaultTestExamples).
	maxExamples int
	// retryTemperature, when set (task_args "retry_temperature"), replaces
	// "temperature" in the params handed to Prepare on any attempt after the
	// first (ConversionRequest.CurrentAttempt > 1) - an opt-in sampling bump
	// so a resampled retry of a near-greedy model actually explores a
	// different completion instead of reproducing the same wrong output
	// (self-consistency, Wang et al. arXiv:2203.11171). nil means unset: no
	// override, taskParams passed through unchanged as before.
	retryTemperature *float64
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

// paramsForAttempt returns the params to hand to Client.Prepare for this
// call: cc.taskParams unchanged, unless retry_temperature is configured and
// this is a resample-style retry (attempt > 1), in which case it returns a
// fresh copy with "temperature" overridden. cc.taskParams itself is never
// mutated - it's a long-lived map reused on every future attempt and every
// future request this converter processes, so bumping it in place would leak
// the retry temperature into the next unrelated first attempt too.
func (cc *LLMConverter) paramsForAttempt(attempt int) map[string]interface{} {
	if cc.retryTemperature == nil || attempt <= 1 {
		return cc.taskParams
	}
	params := make(map[string]interface{}, len(cc.taskParams)+1)
	maps.Copy(params, cc.taskParams)
	params["temperature"] = *cc.retryTemperature
	return params
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

	maxExamples := defaultTestExamples
	switch v := taskParams["max_test_examples"].(type) {
	case int:
		if v > 0 {
			maxExamples = v
		}
	case float64:
		if v > 0 {
			maxExamples = int(v)
		}
	}

	var retryTemperature *float64
	switch v := taskParams["retry_temperature"].(type) {
	case float64:
		retryTemperature = &v
	case int:
		f := float64(v)
		retryTemperature = &f
	}

	// converter-level config keys must not leak into the connectors' API
	// params (see Client.Prepare)
	delete(taskParams, "prompt")
	delete(taskParams, "reader")
	delete(taskParams, "mode")
	delete(taskParams, "max_test_examples")
	delete(taskParams, "retry_temperature")

	log.Debugf("creating LLM converter with params: %v", taskParams)
	return &LLMConverter{
		template:         promptTmpl,
		reader:           reader,
		mode:             mode,
		maxExamples:      maxExamples,
		retryTemperature: retryTemperature,
		taskParams:       taskParams,
	}
}

// Apply renders the prompt, calls the runner LLM client and updates the supplied
// ConversionRequest with the resulting DeploymentPackage.
func (cc *LLMConverter) Apply(runner *pipeline.Runner, code *domain.ConversionRequest) error {
	var codePrompt bytes.Buffer

	codeBlock := codeBlockGenerator(code.WorkingPackage)
	tests := sortedTestCases(code)
	firstInput, firstOutput := "", ""
	if len(tests) > 0 {
		firstInput = string(tests[0].Payload)
		firstOutput = string(tests[0].ExpectedOutput)
	}

	srcFile := ""
	if code.SourcePackage != nil {
		srcFile = code.SourcePackage.RootFile
	}

	errStr := ""
	if last := code.LastError(); last != nil {
		errStr = last.Error()
	}

	// The most recent test-stage failure evidence (if any), rendered for
	// prompts as {{ .failures }}: repair works best when the model sees
	// which input produced which wrong output, not just a failure count.
	failuresBlock := renderTestFailures(latestTestFailures(code.Errors()))

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
	templateVars["failures"] = failuresBlock
	templateVars["original"] = srcFile
	templateVars["tests"] = renderTestExamples(tests, cc.maxExamples)
	templateVars["input"] = firstInput
	templateVars["output"] = firstOutput

	err := cc.template.Execute(&codePrompt, templateVars)
	if err != nil {
		code.AddError(err)
		return err
	}

	client := runner.LLMClient()
	if err := client.Prepare(cc.paramsForAttempt(code.CurrentAttempt)); err != nil {
		return domain.NewLLMError(fmt.Errorf("failed to configure LLMClient: %+v", err))
	}

	response, metrics, err := client.InvokeLLM(runner, codePrompt)
	if code.Metrics != nil {
		code.Metrics.AddMetric(metrics)
		// attribute the call's token spend to the currently running task,
		// including failed calls (they cost tokens too)
		code.Metrics.RecordLLMCall(code.CurrentTask, metrics)
	}
	if err != nil {
		// Wrap as LLMError so executeTask can tell infrastructure failures
		// (API outage, rate limit, truncation) from code defects and skip
		// recovery prompts that cannot fix them.
		return domain.NewLLMError(fmt.Errorf("LLM invocation failed: %w", err))
	}

	// Prefer the model the connector reports actually using: model_name is
	// not the key every backend reads (Gemini resolves GEMINI_MODEL), so the
	// task params alone would label a Gemini chatlog "unknown-model".
	modelName := metrics.Model
	if modelName == "" {
		modelName, _ = cc.taskParams["model_name"].(string)
	}
	if modelName == "" {
		modelName = "unknown-model"
	}
	// Label chatlogs with request id and task id so a log file can be
	// mapped back to its job and pipeline stage.
	label := modelName
	if code.CurrentTask != "" {
		label = code.CurrentTask + "_" + label
	}
	if code.Id != uuid.Nil {
		label = code.Id.String()[:8] + "_" + label
	}
	llmconnector.LogResponse(label, codePrompt.String(), response)

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

// latestTestFailures returns the failure evidence of the most recent
// TestingError in the request's error history, or nil when no test stage
// has recorded evidence yet (e.g. before the first goTester run).
func latestTestFailures(errs []error) []domain.TestFailure {
	for i := len(errs) - 1; i >= 0; i-- {
		var te domain.TestingError
		if errors.As(errs[i], &te) && len(te.Failures()) > 0 {
			return te.Failures()
		}
	}
	return nil
}

// renderTestFailures formats failure evidence into the fixed, deterministic
// block prompts consume via {{ .failures }}. Empty when there is none, so
// templates can branch with {{ if .failures }}.
func renderTestFailures(failures []domain.TestFailure) string {
	if len(failures) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range failures {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Test case %q (%s):\n", f.Name, f.Kind)
		if f.Input != "" {
			fmt.Fprintf(&b, "  Input: %s\n", f.Input)
		}
		if f.Expected != "" {
			fmt.Fprintf(&b, "  Expected output: %s\n", f.Expected)
		}
		actual := f.Actual
		if actual == "" {
			actual = "(empty)"
		}
		fmt.Fprintf(&b, "  Actual output: %s\n", actual)
		if f.Detail != "" {
			fmt.Fprintf(&b, "  Mismatch: %s\n", f.Detail)
		}
		if f.Stderr != "" {
			fmt.Fprintf(&b, "  Stderr: %s\n", f.Stderr)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// defaultTestExamples is how many test cases {{ .tests }} shows a prompt
// unless the task sets max_test_examples.
const defaultTestExamples = 3

// sortedTestCases returns the source package's parseable test fixtures in
// lexical file order, parsed through the canonical fixture schema (so a rich
// payload/expectedOutput fixture renders into prompts the same way a lowered
// legacy one does). Map iteration order is randomized in Go, so without
// sorting the "first" test shown to a prompt could differ between runs and
// retries, making experiments non-reproducible.
func sortedTestCases(code *domain.ConversionRequest) []fixture.TestCase {
	if code.SourcePackage == nil {
		return nil
	}
	names := slices.Sorted(maps.Keys(code.SourcePackage.TestFiles))
	out := make([]fixture.TestCase, 0, len(names))
	for _, name := range names {
		tc, err := fixture.Parse(name, []byte(code.SourcePackage.TestFiles[name]))
		if err != nil {
			log.Debugf("skipping unparseable test fixture %s for prompt context: %v", name, err)
			continue
		}
		out = append(out, tc)
	}
	return out
}

// renderTestExamples formats up to limit test fixtures as input/expected
// pairs for {{ .tests }}. Multiple cases matter: the error-path fixture is
// often the only specification of the non-happy-path statusCode mapping.
func renderTestExamples(tests []fixture.TestCase, limit int) string {
	if limit <= 0 {
		limit = defaultTestExamples
	}
	if len(tests) > limit {
		tests = tests[:limit]
	}
	var b strings.Builder
	for i, tc := range tests {
		if i > 0 {
			b.WriteString("\n")
		}
		expected := string(tc.ExpectedOutput)
		if expected == "" {
			expected = "(unspecified - side effects are validated separately)"
		}
		fmt.Fprintf(&b, "Test case %d:\n  Input: %s\n  Expected output: %s\n", i+1, capForPrompt(string(tc.Payload)), capForPrompt(expected))
	}
	return strings.TrimRight(b.String(), "\n")
}

// capForPrompt bounds a fixture field so a large payload fixture cannot
// blow up the prompt.
func capForPrompt(s string) string {
	const maxLen = 2000
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... [truncated]"
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
