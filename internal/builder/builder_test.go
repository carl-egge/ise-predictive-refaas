package builder

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestExtractDiagnostics guards the D3/E4 behavior: compiler and module
// diagnostics are pulled out of raw build output, de-duplicated and capped,
// so the fixer prompt gets a focused numbered list instead of a log dump.
func TestExtractDiagnostics(t *testing.T) {
	// real output shape from examples/metrics/metrics-20260624111549.json
	output := "# github.com/lambda/function\n" +
		"./main.go:11:50: syntax error: unexpected name Num2 in struct type; possibly missing semicolon or newline or }\n" +
		"./main.go:11:50: syntax error: unexpected name Num2 in struct type; possibly missing semicolon or newline or }\n" + // duplicate
		"exit status 1\n"

	diags := extractDiagnostics(output)
	if len(diags) != 1 {
		t.Fatalf("extractDiagnostics = %d entries, want 1 (duplicate and noise dropped): %v", len(diags), diags)
	}
	if !strings.Contains(diags[0], "main.go:11:50") {
		t.Errorf("diagnostic should keep the position, got: %s", diags[0])
	}

	// go.mod parse errors (observed failure class) are kept verbatim
	modOutput := "go: errors parsing go.mod:\ngo.mod:6: unknown directive: golang.org/x/exp/cmd/godoc\n"
	diags = extractDiagnostics(modOutput)
	if len(diags) != 2 {
		t.Fatalf("extractDiagnostics(go.mod) = %d entries, want 2: %v", len(diags), diags)
	}

	// cascade capping: 7 distinct errors -> 5 plus an omission marker
	var cascade strings.Builder
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(&cascade, "./main.go:%d:1: undefined: sym%d\n", i, i)
	}
	diags = extractDiagnostics(cascade.String())
	if len(diags) != maxCompilerErrors+1 {
		t.Fatalf("extractDiagnostics(cascade) = %d entries, want %d+omission", len(diags), maxCompilerErrors)
	}
	if !strings.Contains(diags[maxCompilerErrors], "omitted") {
		t.Errorf("last entry should note the omission, got: %s", diags[maxCompilerErrors])
	}

	if got := extractDiagnostics("random tool banner\nnothing parseable"); got != nil && len(got) != 0 {
		t.Errorf("unparseable output should yield no diagnostics, got %v", got)
	}
}

// TestFormatBuildErrorKeepsGoModMarkers verifies the structured error still
// contains the markers isGoModFailure matches on, so the deterministic
// go.mod regeneration fallback keeps triggering.
func TestFormatBuildErrorKeepsGoModMarkers(t *testing.T) {
	output := "go: errors parsing go.mod:\ngo.mod:6: unknown directive: golang.org/x/exp/cmd/godoc\n"
	err := formatBuildError("go mod tidy", output, errors.New("exit status 1"))

	if !isGoModFailure(err) {
		t.Errorf("formatted error must still be detectable as a go.mod failure: %v", err)
	}
	if !strings.Contains(err.Error(), "1. ") {
		t.Errorf("error should be a numbered list, got: %v", err)
	}

	// unparseable output falls back to the raw format
	raw := formatBuildError("go build", "linker exploded in some novel way", errors.New("exit status 2"))
	if !strings.Contains(raw.Error(), "linker exploded") {
		t.Errorf("fallback should preserve the raw output, got: %v", raw)
	}
}

// TestFormatBuildErrorStripsRedundantExitStatus guards [B3]: a plain
// *exec.ExitError ("exit status N") carries no information beyond what the
// captured combined stdout/stderr already shows, so it must not be appended a
// second time to the fallback message. A non-exit-status error (e.g. the
// command failing to start) is unique information and must still appear.
func TestFormatBuildErrorStripsRedundantExitStatus(t *testing.T) {
	// a real *exec.ExitError, since the fallback path type-asserts for it
	exitErr := exec.Command("false").Run()
	if exitErr == nil {
		t.Fatal("expected the `false` command to exit non-zero")
	}

	err := formatBuildError("go build", "unparseable linker noise", exitErr)
	if strings.Contains(err.Error(), "exit status") {
		t.Errorf("redundant exit status suffix should be stripped, got: %v", err)
	}

	other := errors.New(`exec: "go": executable file not found in $PATH`)
	err = formatBuildError("go build", "", other)
	if !strings.Contains(err.Error(), "executable file not found") {
		t.Errorf("a non-exit-status error carries unique information and must be kept, got: %v", err)
	}
}
