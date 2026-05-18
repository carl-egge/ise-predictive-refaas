// Package main contains simple error wrapper types used to surface
// domain-specific error kinds from conversion steps.
package main

// TestingError represents a failure during the test execution
// and carries an associated error code.
type TestingError struct {
	error
	error_code int
}

func (e TestingError) Error() string {
	return e.error.Error()
}

// CompilationError wraps errors produced during compilation.
type CompilationError struct {
	error
}

func (e CompilationError) Error() string {
	return e.error.Error()
}

// LLMError wraps errors coming from LLM invocation or parsing.
type LLMError struct {
	error
}

func (e LLMError) Error() string {
	return e.error.Error()
}
