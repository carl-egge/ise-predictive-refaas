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

// tidyFailureOutput is real `go mod tidy` output, captured verbatim from the
// toolchain for the import set of f16 (evaluation_set) with its one invented
// path, "service/iotdata" (the real module is service/iotdataplane).
//
// The shape is what matters: one "finding module for package" line per import
// whether or not it resolves, one "found ... in ..." per module that does, and
// only at the very end the two-line block naming the package that does not -
// whose second line is indented with a tab rather than prefixed "go: ".
const tidyFailureOutput = `go: finding module for package github.com/aws/aws-sdk-go-v2/service/iotdata
go: finding module for package github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue
go: finding module for package github.com/aws/aws-sdk-go-v2/aws
go: finding module for package github.com/aws/aws-sdk-go-v2/config
go: finding module for package github.com/aws/aws-sdk-go-v2/service/dynamodb/types
go: finding module for package github.com/aws/aws-sdk-go-v2/service/dynamodb
go: downloading github.com/aws/aws-sdk-go-v2/config v1.33.2
go: found github.com/aws/aws-sdk-go-v2/aws in github.com/aws/aws-sdk-go-v2 v1.45.1
go: found github.com/aws/aws-sdk-go-v2/config in github.com/aws/aws-sdk-go-v2/config v1.33.2
go: found github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue in github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.21.2
go: found github.com/aws/aws-sdk-go-v2/service/dynamodb in github.com/aws/aws-sdk-go-v2/service/dynamodb v1.66.0
go: found github.com/aws/aws-sdk-go-v2/service/dynamodb/types in github.com/aws/aws-sdk-go-v2/service/dynamodb v1.66.0
go: finding module for package github.com/aws/aws-sdk-go-v2/service/iotdata
go: example.com imports
	github.com/aws/aws-sdk-go-v2/service/iotdata: module github.com/aws/aws-sdk-go-v2@latest found (v1.45.1), but does not contain package github.com/aws/aws-sdk-go-v2/service/iotdata
`

// TestExtractDiagnosticsSurfacesTheModuleCause is the regression test for
// [C13]. In run 20260831-190900 the fixer for f16 received five
// "finding module for package <valid path>" lines and "... further errors
// omitted" - a list in which nothing was actually wrong - and burned all four
// builder attempts on it. Two independent defects produced that: progress
// chatter consumed the whole cap, and the indented line naming the bad package
// was discarded by the prefix test regardless of the cap.
func TestExtractDiagnosticsSurfacesTheModuleCause(t *testing.T) {
	diags := extractDiagnostics(tidyFailureOutput)

	if len(diags) == 0 {
		t.Fatal("no diagnostics extracted from a real tidy failure")
	}
	joined := strings.Join(diags, "\n")

	// The whole point: the offending package and the reason must be present.
	if !strings.Contains(joined, "service/iotdata") {
		t.Errorf("the offending import is missing; the fixer cannot fix what it is not shown:\n%s", joined)
	}
	if !strings.Contains(joined, "does not contain package") {
		t.Errorf("the reason is missing - it lives on the indented continuation line:\n%s", joined)
	}
	// Progress chatter must not appear at all: it is what crowded the cause out.
	for _, noise := range []string{"finding module for package", "downloading ", "found github.com"} {
		if strings.Contains(joined, noise) {
			t.Errorf("progress line %q reached the fixer prompt:\n%s", noise, joined)
		}
	}
	// One import failed, so one diagnostic is the honest answer.
	if len(diags) != 1 {
		t.Errorf("extractDiagnostics = %d entries, want 1 for a single unresolvable import: %v", len(diags), diags)
	}
	// A valid import must not be named: doing so is what sent the fixer after
	// correct code.
	if strings.Contains(joined, "service/dynamodb/types") {
		t.Errorf("a correctly-resolved import was reported as a problem:\n%s", joined)
	}
}

// TestExtractDiagnosticsFoldsCompilerExplanations covers the other half of the
// continuation fix: since Go 1.18 the compiler puts the *reason* for a type
// error on an indented line, which the previous prefix test dropped.
func TestExtractDiagnosticsFoldsCompilerExplanations(t *testing.T) {
	output := "# github.com/lambda/function\n" +
		"./main.go:42:9: cannot use c (variable of type *s3.Client) as Storer value in argument to save:\n" +
		"\t*s3.Client does not implement Storer (missing method Put)\n"

	diags := extractDiagnostics(output)
	if len(diags) != 1 {
		t.Fatalf("extractDiagnostics = %d entries, want 1: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0], "missing method Put") {
		t.Errorf("the explanation should be folded into its diagnostic, got: %s", diags[0])
	}
	if !strings.Contains(diags[0], "main.go:42:9") {
		t.Errorf("the position should be preserved, got: %s", diags[0])
	}
}

// TestExtractDiagnosticsCapsClassesSeparately pins why there are two caps: the
// compiler-cascade argument for capping at 5 does not apply to module errors,
// where each block is an independent unresolvable import and the last one is as
// likely to be the culprit as the first.
func TestExtractDiagnosticsCapsClassesSeparately(t *testing.T) {
	var mixed strings.Builder
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(&mixed, "./main.go:%d:1: undefined: sym%d\n", i, i)
	}
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(&mixed, "go: example.com imports\n\tbogus/pkg%d: module bogus@latest found, but does not contain package bogus/pkg%d\n", i, i)
	}

	diags := extractDiagnostics(mixed.String())
	joined := strings.Join(diags, "\n")

	// 5 compiler + its omission note, then the module blocks (deduplicated by
	// their shared "go: example.com imports" line, so one entry survives).
	if !strings.Contains(joined, "undefined: sym5") || strings.Contains(joined, "undefined: sym6") {
		t.Errorf("compiler errors should cap at %d, got:\n%s", maxCompilerErrors, joined)
	}
	if !strings.Contains(joined, "2 more errors omitted") {
		t.Errorf("the omission note should say how many were dropped, got:\n%s", joined)
	}
	if !strings.Contains(joined, "does not contain package") {
		t.Errorf("module errors must survive a compiler cascade, got:\n%s", joined)
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

// TestGoModMarkerSurvivesProgressChatter covers a second consequence of the
// truncation, easy to miss because it is silent: isGoModFailure runs on the
// *formatted* error, so a marker pushed past the cap by progress lines took the
// deterministic go.mod-regeneration fallback (rebuildWithFreshGoMod) with it.
// The build then failed for a reason the pipeline already knows how to repair.
func TestGoModMarkerSurvivesProgressChatter(t *testing.T) {
	var output strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&output, "go: finding module for package example.com/pkg%d\n", i)
		fmt.Fprintf(&output, "go: downloading example.com/pkg%d v1.0.%d\n", i, i)
	}
	output.WriteString("go: updates to go.mod needed; to update it:\n\tmissing go.sum entry for module example.com/pkg1\n")

	err := formatBuildError("go mod tidy", output.String(), errors.New("exit status 1"))
	if !isGoModFailure(err) {
		t.Errorf("the go.mod marker must survive; without it rebuildWithFreshGoMod never runs: %v", err)
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
