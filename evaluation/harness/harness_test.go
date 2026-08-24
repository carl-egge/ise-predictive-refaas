package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The marker is a contract between four files that cannot import each other:
// two of them are templates rather than compilable Go, and the fifth consumer
// lives in internal/builder. A mismatch would not fail any build - it would
// silently break envelope parsing at runtime, which is precisely the failure
// [A18] was about.
func TestOutputMarkerAgreesEverywhere(t *testing.T) {
	files := map[string]string{
		"evaluation/harness/handler.py":           "handler.py",
		"evaluation/harness/bench_handler.go.txt": "bench_handler.go.txt",
		"internal/builder/test_handler.txt":       filepath.Join("..", "..", "internal", "builder", "test_handler.txt"),
		"internal/builder/validator.go":           filepath.Join("..", "..", "internal", "builder", "validator.go"),
	}
	for label, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if !strings.Contains(string(data), OutputMarker) {
			t.Errorf("%s does not contain the shared marker %q; the envelope contract is broken",
				label, OutputMarker)
		}
	}
}

func TestHarnessesAreEmbedded(t *testing.T) {
	if len(Python) == 0 {
		t.Error("python harness is empty")
	}
	if len(GoBench) == 0 {
		t.Error("go bench harness is empty")
	}
	if !strings.Contains(string(GoBench), "func main()") {
		t.Error("go bench harness has no main()")
	}
	if !strings.Contains(string(GoBench), "handle(ctx, event)") {
		t.Error("go bench harness must call handle(), the symbol a translated package defines")
	}
}

// The two harnesses must agree on the envelope, or the two sides of the
// energy comparison are not doing the same work.
func TestBothHarnessesEmitTheSameEnvelopeKeys(t *testing.T) {
	for _, want := range []string{`"response"`, `"error"`} {
		if !strings.Contains(string(Python), want) {
			t.Errorf("python harness never emits %s", want)
		}
		if !strings.Contains(string(GoBench), want) {
			t.Errorf("go bench harness never emits %s", want)
		}
	}
}

// -- Python harness behaviour ---------------------------------------------

func python(t *testing.T) string {
	t.Helper()
	for _, name := range []string{os.Getenv("PYSCAN_PYTHON"), "python3", "python"} {
		if name == "" {
			continue
		}
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("no python3 interpreter on PATH")
	return ""
}

// runPython writes the harness and a function next to each other and feeds it
// the given JSON Lines.
func runPython(t *testing.T, source, stdin string) (string, string, error) {
	t.Helper()
	dir := t.TempDir()
	harnessPath := filepath.Join(dir, "handler.py")
	if err := os.WriteFile(harnessPath, Python, 0o644); err != nil {
		t.Fatal(err)
	}
	fnPath := filepath.Join(dir, "main.py")
	if err := os.WriteFile(fnPath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(python(t), harnessPath, fnPath)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

const echoFunction = `
import json

def lambda_handler(event, context):
    return {"statusCode": 200, "body": json.dumps({"got": event.get("n")})}
`

func TestPythonHarnessOneEnvelopePerPayload(t *testing.T) {
	stdout, stderr, err := runPython(t, echoFunction, "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")
	if err != nil {
		t.Fatalf("harness failed: %v\n%s", err, stderr)
	}
	if got := strings.Count(stdout, OutputMarker); got != 3 {
		t.Errorf("got %d envelopes for 3 payloads, want 3\n%s", got, stdout)
	}
	// Each payload must reach the function distinctly - a harness that read
	// stdin whole would answer all three with the same event.
	for _, want := range []string{`\"got\": 1`, `\"got\": 2`, `\"got\": 3`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %s; payloads are not being read per line\n%s", want, stdout)
		}
	}
}

// stdout is the harness's channel ([A18]): a function that prints must not be
// able to corrupt the envelope.
func TestPythonHarnessKeepsFunctionOutputOffStdout(t *testing.T) {
	noisy := `
import sys, logging

logging.basicConfig(level=logging.INFO)

def lambda_handler(event, context):
    print("chatter on stdout")
    logging.info("chatter via logging")
    sys.stdout.write("more chatter\n")
    return {"ok": True}
`
	stdout, stderr, err := runPython(t, noisy, "{}\n")
	if err != nil {
		t.Fatalf("harness failed: %v\n%s", err, stderr)
	}
	if strings.Contains(stdout, "chatter") {
		t.Errorf("function output leaked onto the response channel:\n%s", stdout)
	}
	if !strings.Contains(stderr, "chatter on stdout") {
		t.Errorf("function output should be captured as diagnostics on stderr, got:\n%s", stderr)
	}
	envelope := stdout[strings.LastIndex(stdout, OutputMarker)+len(OutputMarker):]
	if !strings.Contains(envelope, `"response"`) {
		t.Errorf("envelope is not a clean response object:\n%s", envelope)
	}
}

// A raising function must produce an error envelope rather than killing the
// process, so the driver can tell "unmeasurable" from "crashed harness".
func TestPythonHarnessReportsRaisesAsErrorEnvelope(t *testing.T) {
	raiser := `
def lambda_handler(event, context):
    raise ValueError("boom")
`
	stdout, stderr, err := runPython(t, raiser, "{}\n")
	if err != nil {
		t.Fatalf("a raising function must not fail the harness: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, `"error"`) || !strings.Contains(stdout, "boom") {
		t.Errorf("expected an error envelope naming the exception, got:\n%s", stdout)
	}
}

func TestPythonHarnessResolvesAlternativeHandlerNames(t *testing.T) {
	for _, name := range []string{"lambda_handler", "handler", "main"} {
		source := "def " + name + "(event, context):\n    return {\"via\": \"" + name + "\"}\n"
		stdout, stderr, err := runPython(t, source, "{}\n")
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, stderr)
		}
		if !strings.Contains(stdout, name) {
			t.Errorf("handler named %q was not invoked:\n%s", name, stdout)
		}
	}
}

func TestPythonHarnessFailsWhenNoHandlerExists(t *testing.T) {
	_, stderr, err := runPython(t, "x = 1\n", "{}\n")
	if err == nil {
		t.Fatal("a module with no handler must fail loudly, not measure nothing")
	}
	if !strings.Contains(stderr, "no handler found") {
		t.Errorf("error should name the problem, got:\n%s", stderr)
	}
}

// Module-level statements run at import, before any payload is read, so they
// are charged to startup - which is what makes the cold/steady split mean
// what it claims.
func TestPythonHarnessImportsBeforeReadingPayloads(t *testing.T) {
	source := `
import sys
print("import-time", file=sys.stderr)

def lambda_handler(event, context):
    print("call-time", file=sys.stderr)
    return {}
`
	_, stderr, err := runPython(t, source, "{}\n{}\n")
	if err != nil {
		t.Fatalf("harness failed: %v\n%s", err, stderr)
	}
	if got := strings.Count(stderr, "import-time"); got != 1 {
		t.Errorf("module body ran %d times, want exactly 1 (it belongs to startup)", got)
	}
	if got := strings.Count(stderr, "call-time"); got != 2 {
		t.Errorf("handler ran %d times, want 2", got)
	}
}
