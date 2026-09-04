package domain

import "testing"

// TestRecordFailureCountsConsecutiveRepeats guards [C5]'s core primitive:
// identical text in a row increments, different text resets to 1, and
// different task ids are tracked independently.
func TestRecordFailureCountsConsecutiveRepeats(t *testing.T) {
	cr := &ConversionRequest{}

	if got := cr.RecordFailure("goTester", "same error"); got != 1 {
		t.Errorf("first occurrence = %d, want 1", got)
	}
	if got := cr.RecordFailure("goTester", "same error"); got != 2 {
		t.Errorf("first repeat = %d, want 2", got)
	}
	if got := cr.RecordFailure("goTester", "same error"); got != 3 {
		t.Errorf("second repeat = %d, want 3", got)
	}
	if got := cr.RecordFailure("goTester", "a different error"); got != 1 {
		t.Errorf("changed text should reset to 1, got %d", got)
	}
	if got := cr.RecordFailure("goTester", "a different error"); got != 2 {
		t.Errorf("repeat of the new text = %d, want 2", got)
	}

	// a different task id must not share state with "goTester" above
	if got := cr.RecordFailure("builder", "same error"); got != 1 {
		t.Errorf("a different task id must track independently, got %d", got)
	}
}

// TestRecordFailureSeesThroughPositionDrift replays f0 of the evaluation set,
// which reported the same undefined symbol four times in a row at four
// different coordinates. Byte comparison scored that as four *different*
// failures, so the guard never fired and the job spent its whole builder
// budget regenerating the same defect.
func TestRecordFailureSeesThroughPositionDrift(t *testing.T) {
	cr := &ConversionRequest{}
	f0 := []string{
		"the build command \"go build -o fn .\" failed with the following errors:\n1. ./main.go:101:52: undefined: mail.AddressList\n2. ./main.go:187:22: undefined: ses.RawMessage",
		"the build command \"go build -o fn .\" failed with the following errors:\n1. ./main.go:102:52: undefined: mail.AddressList\n2. ./main.go:188:24: undefined: ses.RawMessage",
		"the build command \"go build -o fn .\" failed with the following errors:\n1. ./main.go:102:74: undefined: mail.AddressList\n2. ./main.go:188:24: undefined: ses.RawMessage",
	}
	want := []int{1, 2, 3}
	for i, text := range f0 {
		if got := cr.RecordFailure("builder", text); got != want[i] {
			t.Errorf("attempt %d counted as %d repeats, want %d - the position moved, the defect did not",
				i+1, got, want[i])
		}
	}
}

// TestRecordFailureSeesThroughDiagnosticOrder covers f16/f26: `go mod tidy`
// emits its lines in nondeterministic order, so the identical failure never
// compared equal and a module-resolution loop could not be detected at all.
func TestRecordFailureSeesThroughDiagnosticOrder(t *testing.T) {
	cr := &ConversionRequest{}
	shuffled := []string{
		"the build command \"go mod tidy\" failed with the following errors:\n1. go: finding module for package a\n2. go: finding module for package b\n3. go: finding module for package c",
		"the build command \"go mod tidy\" failed with the following errors:\n1. go: finding module for package c\n2. go: finding module for package a\n3. go: finding module for package b",
	}
	if got := cr.RecordFailure("builder", shuffled[0]); got != 1 {
		t.Fatalf("first occurrence = %d, want 1", got)
	}
	if got := cr.RecordFailure("builder", shuffled[1]); got != 2 {
		t.Errorf("a reordered but identical diagnostic set = %d repeats, want 2", got)
	}
}

// TestRecordFailureStillDistinguishesRealProgress is the other half of the
// contract, and the one that protects conversions: normalisation must not
// collapse two genuinely different failures, or a repair loop that is
// converging gets aborted. Each pair below moved the pipeline forward in the
// real run.
func TestRecordFailureStillDistinguishesRealProgress(t *testing.T) {
	cases := []struct {
		name  string
		a, b  string
		taskI string
	}{
		{
			// f12: the same tests, but the failures changed kind - which is
			// what a partially-effective realign looks like.
			name:  "failure kind changes",
			a:     "3/4 tests failed: book-car (execution error), book-hotel (execution error)",
			b:     "3/4 tests failed: book-car (execution error), book-hotel (output mismatch)",
			taskI: "goTester",
		},
		{
			// f57: fewer cases failing is unambiguous progress.
			name:  "fewer cases failing",
			a:     "5/5 tests failed: a (output mismatch), b (output mismatch)",
			b:     "2/5 tests failed: a (output mismatch)",
			taskI: "goTester",
		},
		{
			// f38: the fixer switched approach, aws.Bool(x) -> &x. Same line,
			// different attempt - the guard must let it run.
			name:  "different fix attempt at the same position",
			a:     "the build command \"go build\" failed with the following errors:\n1. ./main.go:58:18: cannot use aws.Bool(undo) (value of type *bool) as bool value",
			b:     "the build command \"go build\" failed with the following errors:\n1. ./main.go:58:18: cannot use &undo (value of type *bool) as bool value",
			taskI: "builder",
		},
		{
			name:  "one diagnostic resolved, another remains",
			a:     "the build command \"go build\" failed with the following errors:\n1. ./main.go:10:1: undefined: a\n2. ./main.go:20:1: undefined: b",
			b:     "the build command \"go build\" failed with the following errors:\n1. ./main.go:20:1: undefined: b",
			taskI: "builder",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cr := &ConversionRequest{}
			cr.RecordFailure(tc.taskI, tc.a)
			if got := cr.RecordFailure(tc.taskI, tc.b); got != 1 {
				t.Errorf("counted as %d repeats; a converging repair loop would be aborted", got)
			}
		})
	}
}

func TestNormaliseFailureLeavesSimpleTextAlone(t *testing.T) {
	for _, s := range []string{"", "boom", "some error: with a colon"} {
		if got := normaliseFailure(s); got != s {
			t.Errorf("normaliseFailure(%q) = %q, want it unchanged", s, got)
		}
	}
}
