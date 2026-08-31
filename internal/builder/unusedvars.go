package builder

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
)

// Deterministic repair for Go's "declared and not used" rule.
//
// Why this exists: unused locals were the single largest build-stage failure
// class in the 2026-08-30 evaluation_set run - 10 of 95 functions never
// produced a buildable package because of it, and the LLM fixer repeatedly
// failed to clear them. It is a purely mechanical rule with no judgement in
// it, so spending an LLM call (and a stagnation-guard budget) on it is waste.
//
// This is the same idea as the goimports pass in internal/translator
// (unused *imports*, [C4]); the difference is that unused *variables* need a
// position to act on, which the compiler already hands us, so this runs in
// the build stage off the diagnostics rather than blind on the source.

// declaredNotUsedRe matches a Go "declared and not used" diagnostic. It has to
// cope with both the raw compiler line and the numbered form the builder
// renders into domain.CompilationError ("1. ./main.go:12:3: ..."). Older
// toolchains say "declared but not used"; newer ones quote the name.
var declaredNotUsedRe = regexp.MustCompile(
	`(?m)^\s*(?:\d+\.\s*)?\.?/?[\w./-]*\.go:(\d+):(\d+):\s*declared (?:and|but) not used:?\s*"?([\p{L}_][\p{L}\p{N}_]*)"?`)

// unusedVar is one such diagnostic: a 1-based position and the identifier.
type unusedVar struct {
	Line, Col int
	Name      string
}

// unusedVarDiagnostics extracts every "declared and not used" position from a
// build error. Diagnostics for other files are matched too - the caller only
// applies them to the source it holds, and the root file is the only Go source
// the pipeline produces.
func unusedVarDiagnostics(msg string) []unusedVar {
	var out []unusedVar
	for _, m := range declaredNotUsedRe.FindAllStringSubmatch(msg, -1) {
		line, err1 := strconv.Atoi(m[1])
		col, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil || line <= 0 || col <= 0 {
			continue
		}
		out = append(out, unusedVar{Line: line, Col: col, Name: m[3]})
	}
	return out
}

// stripUnusedVars rewrites each reported identifier to the blank identifier,
// which removes the variable while preserving the right-hand side - deleting
// the statement outright would drop a call whose side effects the function
// may depend on (`resp, err := client.Put(...)`).
//
// It reports whether anything changed. A source that does not parse is
// returned untouched: this repair is best effort, exactly like the goimports
// pass, and a syntax error is the LLM fixer's job, not this function's.
func stripUnusedVars(src string, vars []unusedVar) (string, bool) {
	if len(vars) == 0 {
		return src, false
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, parser.ParseComments)
	if err != nil {
		return src, false
	}
	tf := fset.File(file.Pos())
	if tf == nil {
		return src, false
	}

	// Resolve each diagnostic to the exact token position the compiler named.
	want := make(map[token.Pos]string, len(vars))
	for _, v := range vars {
		if v.Line > tf.LineCount() {
			continue
		}
		pos := tf.LineStart(v.Line) + token.Pos(v.Col-1)
		if pos <= tf.Pos(0) || pos > tf.Pos(tf.Size()) {
			continue
		}
		want[pos] = v.Name
	}
	if len(want) == 0 {
		return src, false
	}

	changed := false
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		// Match on name as well as position: if the position does not resolve
		// to the identifier the compiler named, the source has moved under us
		// and blanking whatever sits there would corrupt the file.
		if name, ok := want[id.Pos()]; ok && name == id.Name {
			id.Name = "_"
			changed = true
		}
		return true
	})
	if !changed {
		return src, false
	}

	// `_ := f()` is not legal Go: a short variable declaration must introduce
	// at least one new name. Once every name on the left is blank, the
	// statement has to become a plain assignment.
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Tok != token.DEFINE {
			return true
		}
		for _, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name != "_" {
				return true
			}
		}
		as.Tok = token.ASSIGN
		return true
	})

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return src, false
	}
	return buf.String(), true
}
