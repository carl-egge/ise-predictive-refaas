package translator

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

func parses(t *testing.T, src string) bool {
	t.Helper()
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, "main.go", src, parser.AllErrors)
	return err == nil
}

// TestPostProcessAddsMissingPackageClause covers the exact failure recorded
// in examples/metrics: "main.go:1:1: expected 'package', found 'import'".
// Deterministically fixable, so it should never cost a build round trip plus
// an LLM repair call.
func TestPostProcessAddsMissingPackageClause(t *testing.T) {
	src := `import "fmt"

func handle() { fmt.Println("hi") }
`
	out := postProcessGoSource(src)

	if !strings.HasPrefix(strings.TrimSpace(out), "package main") {
		t.Fatalf("expected a package clause to be added, got:\n%s", out)
	}
	if !parses(t, out) {
		t.Fatalf("output should parse, got:\n%s", out)
	}
}

// TestPostProcessRenamesForeignPackage: any package other than main breaks
// the build against the harness ("found packages X and main"), and renaming
// is mechanical.
func TestPostProcessRenamesForeignPackage(t *testing.T) {
	src := `package handler

func handle() {}
`
	out := postProcessGoSource(src)

	if !strings.Contains(out, "package main") || strings.Contains(out, "package handler") {
		t.Errorf("expected the package to be renamed to main, got:\n%s", out)
	}
	if !parses(t, out) {
		t.Errorf("output should parse, got:\n%s", out)
	}
}

// TestPostProcessRemovesUnusedImports: Go rejects an unused import at compile
// time, so a single stray one from the model is otherwise a build failure.
func TestPostProcessRemovesUnusedImports(t *testing.T) {
	src := `package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func handle() { fmt.Println("hi") }
`
	out := postProcessGoSource(src)

	if strings.Contains(out, `"net/http"`) || strings.Contains(out, `"encoding/json"`) {
		t.Errorf("unused imports should be removed, got:\n%s", out)
	}
	if !strings.Contains(out, `"fmt"`) {
		t.Errorf("the used import must be kept, got:\n%s", out)
	}
}

// TestPostProcessAddsMissingStdlibImport: the mirror case - the model uses a
// package it forgot to import.
func TestPostProcessAddsMissingStdlibImport(t *testing.T) {
	src := `package main

func handle() { fmt.Println("hi") }
`
	out := postProcessGoSource(src)

	if !strings.Contains(out, `"fmt"`) {
		t.Errorf("expected the missing stdlib import to be added, got:\n%s", out)
	}
	if !parses(t, out) {
		t.Errorf("output should parse, got:\n%s", out)
	}
}

// TestPostProcessLeavesBrokenSourceAlone pins the maintainer's rule: a syntax
// error that survives deterministic repair must not be "fixed" here and must
// not fail the stage. The code goes on to the build stage, whose compiler
// diagnostic is the precise error the fixer then repairs - resampling the
// translator instead would just roll a different broken generation.
func TestPostProcessLeavesBrokenSourceAlone(t *testing.T) {
	src := `package main

func handle( {
	this is not go
`
	out := postProcessGoSource(src)

	if out != src {
		t.Errorf("unparseable source should be returned unchanged, got:\n%s", out)
	}
}

// TestPostProcessBrokenSourceStillGetsPackageClause: the two repairs are
// independent - a file that is both missing its package clause and broken
// further down still gets the clause, since that is what makes the compiler
// report the *real* syntax error instead of "expected 'package'".
func TestPostProcessBrokenSourceStillGetsPackageClause(t *testing.T) {
	src := `import "fmt"

func handle( {
`
	out := postProcessGoSource(src)

	if !strings.HasPrefix(out, "package main") {
		t.Errorf("expected the package clause even on unparseable source, got:\n%s", out)
	}
}

func TestPostProcessEmptyInput(t *testing.T) {
	if got := postProcessGoSource(""); got != "" {
		t.Errorf("empty input should stay empty, got %q", got)
	}
}

// TestGoReaderAppliesPostProcessing verifies the repairs are wired into the
// reader, so every Go-producing stage benefits without pipeline config.
func TestGoReaderAppliesPostProcessing(t *testing.T) {
	reader := GoJsonOllamaReader{}
	original := &domain.DeploymentPackage{Suffix: "py"}
	response := `{"main.go": "import \"fmt\"\n\nfunc handle() { fmt.Println(\"hi\") }\n"}`

	dp, err := reader.MakeDeploymentFile(response, original)
	if err != nil {
		t.Fatalf("MakeDeploymentFile: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(dp.RootFile), "package main") {
		t.Errorf("reader did not apply post-processing:\n%s", dp.RootFile)
	}
	if !parses(t, dp.RootFile) {
		t.Errorf("reader output should parse:\n%s", dp.RootFile)
	}
}

// TestGoReaderPostProcessingKeepsBrokenCode: the reader must still hand
// unparseable code on (for the build stage to diagnose) rather than erroring,
// which would fail the convert task and re-sample the translator.
func TestGoReaderPostProcessingKeepsBrokenCode(t *testing.T) {
	reader := GoJsonOllamaReader{}
	original := &domain.DeploymentPackage{Suffix: "py"}
	response := `{"main.go": "package main\n\nfunc handle( {\n"}`

	dp, err := reader.MakeDeploymentFile(response, original)
	if err != nil {
		t.Fatalf("unparseable code must not fail the reader: %v", err)
	}
	if !strings.Contains(dp.RootFile, "func handle(") {
		t.Errorf("the model's code should be preserved verbatim:\n%s", dp.RootFile)
	}
}
