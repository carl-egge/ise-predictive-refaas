package builder

import "testing"

// TestSimilarityValidationDirection guards against the inverted comparison
// where identical output failed validation and disjoint output passed
// (validate used to return sim < threshold instead of sim >= threshold).
func TestSimilarityValidationDirection(t *testing.T) {
	v := SimilarityValidation{}

	if !v.validate("hello world", "hello world") {
		t.Error("identical strings must pass validation")
	}
	if v.validate(`{"totally":"different"}`, "expected output") {
		t.Error("disjoint strings must fail validation")
	}
	if !v.validateUndeterministic("hello world", "hello world") {
		t.Error("identical strings must pass undeterministic validation")
	}
}

// TestJsonAwareValidateHappyPath compares a harness-wrapped Go response
// against an expected fixture in the canonical (paper) format.
func TestJsonAwareValidateHappyPath(t *testing.T) {
	v := MakeAwareSimilarityValidation(0.85)

	expected := `{"statusCode": 200, "body": "{\"result\": 3}"}`
	actual := `{"response":{"statusCode":200,"headers":null,"body":"{\"result\":3}"}}`
	if !v.validate(actual, expected) {
		t.Error("matching wrapped response must pass validation")
	}

	wrongStatus := `{"response":{"statusCode":500,"body":"{\"result\":3}"}}`
	if v.validate(wrongStatus, expected) {
		t.Error("mismatching statusCode must fail validation")
	}
}

// TestJsonAwareValidateChecksAllSiblingKeys guards against the early-return
// bug where a matching nested object caused all remaining expected keys to be
// skipped (nondeterministically, due to map iteration order).
func TestJsonAwareValidateChecksAllSiblingKeys(t *testing.T) {
	v := MakeAwareSimilarityValidation(0.85)

	expected := `{"a": {"x": 1}, "b": 2}`
	actual := `{"a": {"x": 1}, "b": 3}`
	// run repeatedly: the old bug only surfaced when "a" was iterated first
	for i := 0; i < 50; i++ {
		if v.validate(actual, expected) {
			t.Fatal("mismatching sibling key must fail validation even when a nested object matches")
		}
	}
}

// TestJsonAwareValidateDoesNotPanicOnTypeMismatch guards against unchecked
// type assertions: a scalar "response", or expected/actual leaves of
// different JSON types, must produce a mismatch, not a panic that aborts the
// whole conversion run.
func TestJsonAwareValidateDoesNotPanicOnTypeMismatch(t *testing.T) {
	v := MakeAwareSimilarityValidation(0.85)

	// handler returned a scalar where an object was expected
	if v.validate(`{"response":"just a string"}`, `{"statusCode": 200}`) {
		t.Error("scalar response vs object expectation must fail validation")
	}
	// number vs string leaf
	if v.validate(`{"response":{"statusCode":"200"}}`, `{"statusCode": 200}`) {
		t.Error("string statusCode vs numeric expectation must fail validation")
	}
	// string vs number leaf (reverse direction)
	if v.validate(`{"response":{"statusCode":200}}`, `{"statusCode": "200"}`) {
		t.Error("numeric statusCode vs string expectation must fail validation")
	}
}
