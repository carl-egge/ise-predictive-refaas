package translator

import (
	"go/parser"
	"go/token"

	log "github.com/sirupsen/logrus"
	"golang.org/x/tools/imports"
)

// goRootFileName is the name the builder writes the root source to; used as
// the filename hint for parsing and import resolution.
const goRootFileName = "main.go"

// postProcessGoSource applies the deterministic repairs that would otherwise
// cost a full build round trip plus an LLM fixer call ([C4]):
//
//  1. a missing (or non-main) package clause is inserted/corrected - an
//     observed failure mode, "main.go:1:1: expected 'package', found 'import'";
//  2. imports are resolved with goimports, which both adds missing ones and
//     removes unused ones. Go treats an unused import as a compile error, so
//     a single stray import is otherwise a build failure the model has to be
//     asked to fix.
//
// It is best effort and never fails: source that still does not parse is
// returned unchanged, deliberately. A syntax error that survives this step
// must NOT re-sample the translator - resampling a broken generation tends to
// produce a differently broken one - so the code is handed on to the build
// stage, whose compiler diagnostic is both more precise than go/parser's and
// already routed to the fixer as {{ .issue }} (see [D3]).
func postProcessGoSource(source string) string {
	if source == "" {
		return source
	}

	fixed := ensureMainPackage(source)

	// goimports also gofmts, so the file the model sees on a later repair
	// round is normalized rather than however the model happened to indent.
	formatted, err := imports.Process(goRootFileName, []byte(fixed), nil)
	if err != nil {
		// Unparseable: keep the best version we have and let the build stage
		// produce the authoritative syntax error for the fixer.
		log.Debugf("go post-processing left the source unchanged (it does not parse yet): %v", err)
		return fixed
	}
	return string(formatted)
}

// ensureMainPackage guarantees the file opens with "package main".
//
// A missing clause makes the file unparseable, and a clause naming any other
// package breaks the build differently ("found packages X and main"), because
// the builder writes its own test harness as package main into the same
// directory. Both are mechanical to fix and neither needs a model.
func ensureMainPackage(source string) string {
	fset := token.NewFileSet()
	// PackageClauseOnly stops after the clause, so a syntax error further
	// down the file does not prevent detecting a valid package name.
	node, err := parser.ParseFile(fset, goRootFileName, source, parser.PackageClauseOnly)
	if err != nil || node.Name == nil {
		log.Debugf("go post-processing: adding missing package clause")
		return "package main\n\n" + source
	}
	if node.Name.Name == "main" {
		return source
	}

	start := fset.Position(node.Name.Pos()).Offset
	end := fset.Position(node.Name.End()).Offset
	if start < 0 || end > len(source) || start >= end {
		return source
	}
	log.Debugf("go post-processing: renaming package %q to main", node.Name.Name)
	return source[:start] + "main" + source[end:]
}
