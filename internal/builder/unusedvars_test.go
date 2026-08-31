package builder

import (
	"strings"
	"testing"
)

func TestUnusedVarDiagnostics(t *testing.T) {
	// The numbered form is what domain.CompilationError renders; the bare form
	// is what the compiler emits. Both must parse.
	msg := `the build command "go build -o fn ." failed with the following errors:
1. ./main.go:322:5: declared and not used: text
2. ./main.go:421:2: declared and not used: encouragement
3. ./main.go:12:9: undefined: smithy.As`
	got := unusedVarDiagnostics(msg)
	if len(got) != 2 {
		t.Fatalf("expected 2 unused-variable diagnostics, got %d: %+v", len(got), got)
	}
	if got[0] != (unusedVar{Line: 322, Col: 5, Name: "text"}) {
		t.Errorf("first diagnostic = %+v", got[0])
	}
	if got[1].Name != "encouragement" {
		t.Errorf("second diagnostic = %+v", got[1])
	}
	if len(unusedVarDiagnostics("./main.go:3:2: undefined: foo")) != 0 {
		t.Error("an unrelated diagnostic must not be treated as an unused variable")
	}
	// Older toolchains word it differently.
	if len(unusedVarDiagnostics(`./main.go:4:2: declared but not used: x`)) != 1 {
		t.Error(`"declared but not used" must be recognised too`)
	}
}

func TestStripUnusedVarsBlanksTheName(t *testing.T) {
	src := `package main

func handle() int {
	a := 1
	b := 2
	return b
}
`
	out, changed := stripUnusedVars(src, []unusedVar{{Line: 4, Col: 2, Name: "a"}})
	if !changed {
		t.Fatal("expected a rewrite")
	}
	// `a := 1` has no other name on the left, so it must become an assignment:
	// `_ := 1` is not legal Go.
	if !strings.Contains(out, "_ = 1") {
		t.Errorf("expected `_ = 1`, got:\n%s", out)
	}
	if strings.Contains(out, "a := 1") {
		t.Errorf("the unused name survived:\n%s", out)
	}
	if !strings.Contains(out, "b := 2") {
		t.Errorf("an unrelated declaration was disturbed:\n%s", out)
	}
}

// The right-hand side must survive: dropping the statement would drop a call
// the rest of the function may depend on for its side effects.
func TestStripUnusedVarsKeepsTheRightHandSide(t *testing.T) {
	src := `package main

func handle() error {
	resp, err := doWork()
	return err
}

func doWork() (int, error) { return 0, nil }
`
	out, changed := stripUnusedVars(src, []unusedVar{{Line: 4, Col: 2, Name: "resp"}})
	if !changed {
		t.Fatal("expected a rewrite")
	}
	if !strings.Contains(out, "doWork()") {
		t.Errorf("the call was dropped:\n%s", out)
	}
	// err is still newly declared here, so := must remain.
	if !strings.Contains(out, "_, err := doWork()") {
		t.Errorf("expected `_, err := doWork()`, got:\n%s", out)
	}
}

func TestStripUnusedVarsAllBlankBecomesAssignment(t *testing.T) {
	src := `package main

func handle() {
	x, y := pair()
}

func pair() (int, int) { return 1, 2 }
`
	out, changed := stripUnusedVars(src, []unusedVar{
		{Line: 4, Col: 2, Name: "x"},
		{Line: 4, Col: 5, Name: "y"},
	})
	if !changed {
		t.Fatal("expected a rewrite")
	}
	if !strings.Contains(out, "_, _ = pair()") {
		t.Errorf("expected `_, _ = pair()`, got:\n%s", out)
	}
}

func TestStripUnusedVarsVarDecl(t *testing.T) {
	src := `package main

func handle() {
	var bucket string
}
`
	out, changed := stripUnusedVars(src, []unusedVar{{Line: 4, Col: 6, Name: "bucket"}})
	if !changed {
		t.Fatal("expected a rewrite")
	}
	if !strings.Contains(out, "var _ string") {
		t.Errorf("expected `var _ string`, got:\n%s", out)
	}
}

// Best effort, like the goimports pass: source that does not parse is the LLM
// fixer's problem, and blanking a position in a file we cannot parse would
// corrupt it.
func TestStripUnusedVarsLeavesUnparseableSourceAlone(t *testing.T) {
	src := "package main\n\nfunc handle( {\n\tx := 1\n}\n"
	out, changed := stripUnusedVars(src, []unusedVar{{Line: 4, Col: 2, Name: "x"}})
	if changed || out != src {
		t.Error("unparseable source must be returned untouched")
	}
}

// A stale or mismatched position must not blank whatever happens to sit there.
func TestStripUnusedVarsIgnoresPositionNameMismatch(t *testing.T) {
	src := `package main

func handle() int {
	keep := 1
	return keep
}
`
	out, changed := stripUnusedVars(src, []unusedVar{{Line: 4, Col: 2, Name: "somethingElse"}})
	if changed || out != src {
		t.Errorf("a position whose identifier does not match must be skipped, got:\n%s", out)
	}
	if _, c := stripUnusedVars(src, []unusedVar{{Line: 9999, Col: 2, Name: "keep"}}); c {
		t.Error("an out-of-range line must be skipped")
	}
}

func TestStripUnusedVarsNoDiagnostics(t *testing.T) {
	src := "package main\n\nfunc handle() {}\n"
	if out, changed := stripUnusedVars(src, nil); changed || out != src {
		t.Error("no diagnostics must mean no rewrite")
	}
}
