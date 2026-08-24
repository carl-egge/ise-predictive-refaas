// Package harness holds the two measurement harnesses used by [H6]'s Go vs.
// Python comparison, and is the single place they live.
//
// They are embedded here rather than copied into cmd/runtime because the
// whole point of [H6] is that the two sides are symmetric, and two harnesses
// that can drift apart are the most direct way to lose that. Keeping them as
// one package's assets means a change to either is visible next to the other,
// and MarkerMatchesBuilder (harness_test.go) pins the one constant they also
// share with the validation harness in internal/builder.
//
// Neither file is compiled here: handler.py is a script, and
// bench_handler.go.txt references handle(), which only exists once it is
// written next to a translated package.
package harness

import _ "embed"

// OutputMarker separates anything the measured function wrote to stdout from
// the harness's response envelope, which follows the last occurrence of this
// line.
//
// Four files must agree on it: internal/builder/test_handler.txt (validation
// harness), internal/builder/validator.go (its reader), and the two harnesses
// embedded below. harness_test.go asserts they do.
const OutputMarker = "__REFAAS_HARNESS_OUTPUT__"

// Python is the Python-side harness: reads JSON Lines on stdin, invokes the
// original function once per line, writes the shared envelope.
//
//go:embed handler.py
var Python []byte

// GoBench is the Go-side harness, compiled together with a translated
// package. Line-oriented like the Python one, so a single process can serve
// the N invocations the two-point cold/steady split needs.
//
//go:embed bench_handler.go.txt
var GoBench []byte
