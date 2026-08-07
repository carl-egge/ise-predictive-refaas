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
