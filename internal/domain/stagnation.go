package domain

import (
	"regexp"
	"sort"
	"strings"
)

// Failure-text normalisation for the repair-loop stagnation guard ([C5]).
//
// RecordFailure originally compared failure text byte for byte, on the stated
// assumption that "a truly stuck repair loop reliably reproduces byte-identical
// text". Run 20260831-190900 falsified that. Of the 20 functions that never
// built, 8 exhausted their retry budget without the guard ever firing - not
// because they were making progress, but because the same defect kept coming
// back wearing a different label:
//
//   - f0 reported `undefined: mail.AddressList` four times at 101:52, 102:52,
//     102:74 and 101:74 - one defect, four coordinates.
//   - f82 alternated the same type error between 69:19 and 73:25.
//   - f16/f26 failed `go mod tidy`, whose progress lines come back in
//     nondeterministic order, so two runs of the identical failure never
//     compared equal. ([C13] shrinks that output to its causal line, which
//     helps; normalising the order is what makes it reliable.)
//
// So the comparison is normalised on two axes, and only those two:
//
//  1. **Position.** `file.go:101:52:` becomes `file.go:`. The same compiler
//     complaint about the same symbol is the same complaint whether it moved a
//     line or a column - and a fixer that keeps regenerating it has not
//     progressed just because the code above it shifted.
//  2. **Order.** The numbered diagnostic list is sorted and de-duplicated, so a
//     toolchain that reports the same set of problems in a different sequence
//     compares equal.
//
// Deliberately *not* normalised: identifiers, types, messages, test names and
// failure kinds. Those are what distinguish one defect from another, and
// collapsing them would abort a repair loop that is genuinely converging.
//
// Validated by replaying the whole run: with these two rules the guard newly
// fires on 8 builder jobs and 1 goTester job, **all of which failed anyway**,
// and on **zero** jobs that succeeded. Four of them (f10, f20, f26, f44) abort
// a full attempt earlier, and the rest at least get recorded as stagnation
// rather than as budget exhaustion, which is the difference between a run log
// that explains itself and one that does not.

var (
	// goPositionRe matches the line:column of a Go diagnostic position,
	// e.g. "./main.go:101:52:" (column optional).
	goPositionRe = regexp.MustCompile(`([^\s:]+\.go):\d+(:\d+)?:`)
	// listItemRe matches formatBuildError's "1. " numbering, which shifts
	// whenever a diagnostic is added or removed above it.
	listItemRe = regexp.MustCompile(`^\s*\d+\.\s*`)
)

// normaliseFailure reduces failure text to the identity of the failure, so
// "the same problem again" compares equal across cosmetic variation. See the
// commentary above for what is and is not normalised.
func normaliseFailure(text string) string {
	text = goPositionRe.ReplaceAllString(text, "$1:")

	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(listItemRe.ReplaceAllString(line, ""))
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	if len(cleaned) < 2 {
		return strings.Join(cleaned, "\n")
	}

	// The first line is the header ("the build command %q failed with the
	// following errors:", "3/5 tests failed: ..."); only the diagnostic list
	// below it is order-independent.
	head, rest := cleaned[0], cleaned[1:]
	sort.Strings(rest)
	out := make([]string, 0, len(rest)+1)
	out = append(out, head)
	for i, line := range rest {
		if i > 0 && line == rest[i-1] {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
