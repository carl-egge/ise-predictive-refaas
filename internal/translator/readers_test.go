package translator

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// TestRemoveGoMainMethodKeepsPackageClause guards against a regression where
// rebuilding the file from node.Decls (which excludes the package clause)
// silently dropped "package main" whenever the LLM's response included its
// own func main(), producing an unbuildable main.go
// ("expected 'package', found 'import'").
func TestRemoveGoMainMethodKeepsPackageClause(t *testing.T) {
	reader := GoJsonOllamaReader{}
	input := `package main

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"net/http"
)

func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error) {
	return events.APIGatewayProxyResponse{StatusCode: http.StatusOK}, nil
}

func main() {
	lambda.Start(handle)
}
`
	out := reader.removeGoMainMethod(input)

	if !strings.HasPrefix(strings.TrimSpace(out), "package main") {
		t.Fatalf("expected output to start with 'package main', got:\n%s", out)
	}
	if strings.Contains(out, "func main(") {
		t.Fatalf("expected func main to be removed, got:\n%s", out)
	}

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "main.go", out, parser.AllErrors); err != nil {
		t.Fatalf("rebuilt file is not valid Go source: %v\n%s", err, out)
	}
}

// TestGoJsonReaderRegeneratesGoModAndDropsChatterKeys guards the C3
// behavior: LLM-provided go.mod/go.sum and non-Go response keys are
// discarded, additional .go sources are kept, and the build command list
// always regenerates the module files deterministically.
func TestGoJsonReaderRegeneratesGoModAndDropsChatterKeys(t *testing.T) {
	reader := GoJsonOllamaReader{}
	original := &domain.DeploymentPackage{
		Suffix:    "py",
		TestFiles: map[string]string{"test/t1.json": "{}"},
		Env:       []string{"REGION=eu-1"},
	}
	response := `{
		"main.go": "package main\n\nfunc handle() {}",
		"helper.go": "package main\n\nfunc helper() {}",
		"go.mod": "module broken\n\nrequire something v1.x",
		"go.sum": "garbage",
		"explanation": "here is my reasoning"
	}`

	dp, err := reader.MakeDeploymentFile(response, original)
	if err != nil {
		t.Fatalf("MakeDeploymentFile: %v", err)
	}
	if _, ok := dp.BuildFiles["go.mod"]; ok {
		t.Error("LLM-provided go.mod must be dropped")
	}
	if _, ok := dp.BuildFiles["go.sum"]; ok {
		t.Error("LLM-provided go.sum must be dropped")
	}
	if _, ok := dp.BuildFiles["explanation"]; ok {
		t.Error("non-file chatter keys must be dropped")
	}
	if _, ok := dp.BuildFiles["helper.go"]; !ok {
		t.Error("additional .go sources must be kept")
	}
	wantCmd := []string{"go mod init example.com", "go mod tidy", "go build -o fn ."}
	if !slices.Equal(dp.BuildCmd, wantCmd) {
		t.Errorf("BuildCmd = %v, want %v", dp.BuildCmd, wantCmd)
	}
	if !slices.Equal(dp.Env, original.Env) {
		t.Errorf("Env = %v, want %v (carried from original)", dp.Env, original.Env)
	}
}

// TestJsonCodeBlockReaderLenientExtraction guards the E2 behavior: schema
// enforcement can silently fail per model, so the parser must recover a JSON
// object from fenced or prose-wrapped responses instead of burning an LLM
// retry, and must fail with a descriptive error otherwise.
func TestJsonCodeBlockReaderLenientExtraction(t *testing.T) {
	cases := []struct {
		name     string
		response string
		wantKey  string
		wantVal  string
	}{
		{
			name:     "plain JSON object",
			response: `{"main.go": "package main"}`,
			wantKey:  "main.go",
			wantVal:  "package main",
		},
		{
			name:     "fenced with language tag and prose",
			response: "Here is the translated code:\n```json\n{\"main.go\": \"package main\"}\n```\nLet me know if you need changes.",
			wantKey:  "main.go",
			wantVal:  "package main",
		},
		{
			name:     "bare object wrapped in prose",
			response: `Sure! {"main.go": "package main"} Hope that helps.`,
			wantKey:  "main.go",
			wantVal:  "package main",
		},
		{
			name:     "braces and escaped quotes inside code values",
			response: `noise {"main.go": "func handle() { s := \"{\" }"} noise`,
			wantKey:  "main.go",
			wantVal:  `func handle() { s := "{" }`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files, err := JsonCodeBlockReader(c.response)
			if err != nil {
				t.Fatalf("JsonCodeBlockReader: %v", err)
			}
			if files[c.wantKey] != c.wantVal {
				t.Errorf("files[%q] = %q, want %q", c.wantKey, files[c.wantKey], c.wantVal)
			}
		})
	}
}

// TestJsonCodeBlockReaderSkipsNonStringValues verifies unexpected non-string
// fields don't sink the usable part of an otherwise valid response.
func TestJsonCodeBlockReaderSkipsNonStringValues(t *testing.T) {
	files, err := JsonCodeBlockReader(`{"main.go": "package main", "steps": ["a", "b"], "confidence": 0.9}`)
	if err != nil {
		t.Fatalf("JsonCodeBlockReader: %v", err)
	}
	if files["main.go"] != "package main" {
		t.Errorf("main.go = %q, want the string value kept", files["main.go"])
	}
	if _, ok := files["steps"]; ok {
		t.Error("non-string values must be skipped, not kept")
	}
}

// TestJsonCodeBlockReaderRejectsNonJSON verifies unparseable responses fail
// with a descriptive error (instead of the old nil map + misleading
// "could not find main" downstream).
func TestJsonCodeBlockReaderRejectsNonJSON(t *testing.T) {
	for _, response := range []string{
		"",
		"I cannot translate this function.",
		`{"main.go": "truncated mid-str`, // no balanced object
	} {
		if _, err := JsonCodeBlockReader(response); err == nil {
			t.Errorf("expected an error for %q", response)
		} else if !strings.Contains(err.Error(), "not a JSON object") {
			t.Errorf("error should say the response was not a JSON object, got: %v", err)
		}
	}
}

// TestRenderTestFailuresAndSelection guards the C1 plumbing: the most
// recent TestingError's evidence is selected from the error history and
// rendered into the fixed block prompts consume.
func TestRenderTestFailuresAndSelection(t *testing.T) {
	failures := []domain.TestFailure{{
		Name:     "test/t1.json",
		Kind:     domain.TestFailureMismatch,
		Input:    `{"a":1}`,
		Expected: `{"result":3}`,
		Actual:   `{"result":"3"}`,
		Detail:   "output mismatch at result",
	}}

	rendered := renderTestFailures(failures)
	for _, want := range []string{"test/t1.json", "output mismatch", `Input: {"a":1}`, `Expected output: {"result":3}`, `Actual output: {"result":"3"}`, "Mismatch: output mismatch at result"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered block missing %q:\n%s", want, rendered)
		}
	}

	history := []error{
		errors.New("older build error"),
		domain.NewTestingErrorWithFailures(errors.New("1/1 tests failed"), failures),
		errors.New("newer build error"),
	}
	got := latestTestFailures(history)
	if len(got) != 1 || got[0].Name != "test/t1.json" {
		t.Errorf("latestTestFailures = %+v, want the TestingError's evidence", got)
	}
	if latestTestFailures([]error{errors.New("no tests yet")}) != nil {
		t.Error("latestTestFailures without a TestingError must return nil")
	}
}

// TestAlignPromptRendersFailureEvidence verifies the D2 template: with
// failure evidence the prompt shows the test cases; without it, it falls
// back to the {{ .issue }} summary.
func TestAlignPromptRendersFailureEvidence(t *testing.T) {
	conv, ok := NewAlignmentConverter(map[string]interface{}{"reader": "go"}).(*LLMConverter)
	if !ok {
		t.Fatal("NewAlignmentConverter did not return an LLMConverter (invalid template?)")
	}

	vars := map[string]interface{}{
		"code":     "#### main.go\n```go\npackage main\n```",
		"original": "def handler(event, context): ...",
		"issue":    "2/3 tests failed: t1 (output mismatch)",
		"failures": "Test case \"t1\" (output mismatch):\n  Input: {}\n  Expected output: 1\n  Actual output: 2",
		"input":    "",
		"output":   "",
	}
	var buf bytes.Buffer
	if err := conv.template.Execute(&buf, vars); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	if !strings.Contains(buf.String(), "failed the following test cases") || !strings.Contains(buf.String(), `Test case "t1"`) {
		t.Errorf("prompt should embed the failure evidence:\n%s", buf.String())
	}

	vars["failures"] = ""
	buf.Reset()
	if err := conv.template.Execute(&buf, vars); err != nil {
		t.Fatalf("template execute (no failures): %v", err)
	}
	if !strings.Contains(buf.String(), "2/3 tests failed") {
		t.Errorf("prompt should fall back to the issue summary:\n%s", buf.String())
	}
}

// TestSortedTestCasesAndRenderExamples guards the C7 behavior: fixtures are
// selected in deterministic lexical order (map iteration is randomized),
// unparseable ones are skipped, and rendering respects the example limit.
// Rich (payload/expectedOutput) fixtures render the same way lowered legacy
// ones do.
func TestSortedTestCasesAndRenderExamples(t *testing.T) {
	req := &domain.ConversionRequest{SourcePackage: &domain.DeploymentPackage{
		TestFiles: map[string]string{
			"test/t3.json":  `{"input":"{\"n\":3}","output":"{\"r\":3}"}`,
			"test/t1.json":  `{"input":"{\"n\":1}","output":"{\"r\":1}"}`,
			"test/bad.json": `not json`,
			"test/t2.json":  `{"payload":{"n":2},"expectedOutput":{"r":2}}`,
		},
	}}

	tests := sortedTestCases(req)
	if len(tests) != 3 {
		t.Fatalf("sortedTestCases = %d entries, want 3 (bad fixture skipped)", len(tests))
	}
	if tests[0].Name != "t1" || tests[2].Name != "t3" {
		t.Errorf("fixtures not in lexical order: %s ... %s", tests[0].Name, tests[2].Name)
	}

	rendered := renderTestExamples(tests, 2)
	if !strings.Contains(rendered, `{"n":1}`) || !strings.Contains(rendered, `{"n":2}`) {
		t.Errorf("rendered examples missing cases:\n%s", rendered)
	}
	if strings.Contains(rendered, `{"n":3}`) {
		t.Errorf("rendered examples should respect the limit of 2:\n%s", rendered)
	}
}

// TestTranslatePromptRendersTests verifies the D1 template: with fixtures
// present the prompt embeds the test cases, otherwise it falls back to the
// single expected-output block.
func TestTranslatePromptRendersTests(t *testing.T) {
	conv, ok := NewCodeConverter(map[string]interface{}{"reader": "go"}).(*LLMConverter)
	if !ok {
		t.Fatal("NewCodeConverter did not return an LLMConverter (invalid template?)")
	}

	vars := map[string]interface{}{
		"code":   "def handler(event, context): ...",
		"tests":  "Test case 1:\n  Input: {\"n\":1}\n  Expected output: {\"r\":1}",
		"output": "",
	}
	var buf bytes.Buffer
	if err := conv.template.Execute(&buf, vars); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	if !strings.Contains(buf.String(), "must satisfy these test cases") || !strings.Contains(buf.String(), `{"n":1}`) {
		t.Errorf("prompt should embed the test cases:\n%s", buf.String())
	}

	vars["tests"] = ""
	vars["output"] = `{"statusCode": 200}`
	buf.Reset()
	if err := conv.template.Execute(&buf, vars); err != nil {
		t.Fatalf("template execute (no tests): %v", err)
	}
	if !strings.Contains(buf.String(), "Expected output for one input") {
		t.Errorf("prompt should fall back to the single-output block:\n%s", buf.String())
	}
}

// TestCodeConverterDefaultsSingleFileSchema verifies the code-producing
// factories request the closed single-main.go response schema unless the
// pipeline config overrides output_keys.
func TestCodeConverterDefaultsSingleFileSchema(t *testing.T) {
	conv := NewCodeConverter(map[string]interface{}{"reader": "go"})
	llmConv, ok := conv.(*LLMConverter)
	if !ok {
		t.Fatalf("expected *LLMConverter, got %T", conv)
	}
	keys, ok := llmConv.TaskParams()["output_keys"].(map[string]interface{})
	if !ok {
		t.Fatal("output_keys default missing on coder task")
	}
	if _, ok := keys["main.go"]; !ok || len(keys) != 1 {
		t.Errorf("output_keys = %v, want exactly {main.go}", keys)
	}

	override := map[string]interface{}{"custom.go": map[string]interface{}{}}
	conv = NewCodeConverter(map[string]interface{}{"output_keys": override})
	llmConv = conv.(*LLMConverter)
	keys, _ = llmConv.TaskParams()["output_keys"].(map[string]interface{})
	if _, ok := keys["custom.go"]; !ok {
		t.Errorf("explicit output_keys must not be overridden, got %v", keys)
	}
}
