package pyscan

import (
	"fmt"
	"sort"
	"strings"
)

// This is [C8]'s half of the scan: the same facts, rendered for a prompt
// instead of a model. Injecting explicit API-mapping hints removes the
// hardest reasoning step - library equivalence - from the model's job, which
// is the "more structure, less reliance on large-model reasoning" tradeoff
// this pipeline needs at 30B scale.

// LibHints renders the Python -> Go API mapping table for the modules this
// function actually imports. Empty when nothing is known, so a prompt
// template can test it with {{ if .lib_hints }}.
func (r *Result) LibHints() string {
	if r == nil {
		return ""
	}
	mappings := Mappings(r.Imports)
	if len(mappings) == 0 {
		return ""
	}

	var b strings.Builder
	for _, m := range mappings {
		fmt.Fprintf(&b, "- %s -> %s", m.Python, m.Go)
		if m.Note != "" {
			fmt.Fprintf(&b, " (%s)", m.Note)
		}
		b.WriteByte('\n')
	}

	// boto3's mapping is generic; naming the services the function actually
	// constructs turns it into a concrete import list.
	if len(r.Boto3Services) > 0 {
		services := append([]string(nil), r.Boto3Services...)
		sort.Strings(services)
		fmt.Fprintf(&b, "- AWS services used: %s\n", strings.Join(services, ", "))
	}

	return strings.TrimRight(b.String(), "\n")
}

// PyFeatures renders the constructs that most often survive a translation
// incorrectly. Only non-zero findings are listed: an empty section is more
// useful to a small model than a wall of "0 decorators, 0 generators".
func (r *Result) PyFeatures() string {
	if r == nil {
		return ""
	}

	var notes []string
	add := func(cond bool, format string, args ...any) {
		if cond {
			notes = append(notes, fmt.Sprintf(format, args...))
		}
	}

	add(r.Metric("n_decorators") > 0,
		"%d decorator(s): Go has no decorator syntax - inline the wrapper's behaviour into the function",
		int(r.Metric("n_decorators")))
	add(r.Metric("n_yield") > 0,
		"generator function(s) using yield: return a slice, or drive a channel, rather than emulating laziness")
	add(r.Metric("n_async_defs")+r.Metric("n_await") > 0,
		"async/await: Go is synchronous by default - a direct sequential translation is usually correct")
	add(r.Metric("n_star_args") > 0,
		"*args/**kwargs: Go needs an explicit parameter list or a struct")
	add(r.Metric("n_comprehensions") > 0,
		"%d comprehension(s): translate to explicit for loops", int(r.Metric("n_comprehensions")))
	add(r.Metric("n_lambdas") > 0,
		"%d lambda(s): translate to func literals", int(r.Metric("n_lambdas")))
	add(r.Metric("n_classes") > 0,
		"%d class(es): translate to structs with methods", int(r.Metric("n_classes")))
	add(r.Metric("module_level_stmts") > 5,
		"%d module-level statements run at import time - put the equivalent in init() or at the top of the handler, not inside it",
		int(r.Metric("module_level_stmts")))
	add(r.Metric("n_except") > 0,
		"%d except handler(s): Go has no exceptions - return errors and check them; a bare except becomes an error check around each fallible call",
		int(r.Metric("n_except")))
	add(r.Metric("n_raise") > 0,
		"%d raise(s): return an error instead of panicking", int(r.Metric("n_raise")))

	if len(r.DynamicCalls) > 0 {
		names := make([]string, 0, len(r.DynamicCalls))
		for name := range r.DynamicCalls {
			names = append(names, name)
		}
		sort.Strings(names)
		notes = append(notes, fmt.Sprintf(
			"dynamic/reflective calls (%s): Go's static typing has no direct equivalent - resolve them to concrete field or map access",
			strings.Join(names, ", ")))
	}

	if infeasible := Infeasible(r.Imports); len(infeasible) > 0 {
		for _, lib := range infeasible {
			notes = append(notes, fmt.Sprintf(
				"%s has no Go equivalent (%s) - a faithful translation may not be possible",
				lib.Python, lib.Note))
		}
	}

	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, n := range notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FeasibilityWarning reports the reason a translation looks doomed before it
// is attempted, or "" when nothing stands out.
//
// This is deliberately a *warning*, not a gate: the prediction module's
// learned gate is [I10]'s job and stays off by default. What this does is
// record the deterministic half of that judgement on every job, so the
// signal exists in the metrics long before a model consumes it.
func (r *Result) FeasibilityWarning() string {
	if r == nil {
		return ""
	}
	infeasible := Infeasible(r.Imports)
	if len(infeasible) == 0 {
		return ""
	}
	names := make([]string, 0, len(infeasible))
	for _, lib := range infeasible {
		names = append(names, lib.Python)
	}
	return fmt.Sprintf("imports %s, which have no realistic Go equivalent", strings.Join(names, ", "))
}
