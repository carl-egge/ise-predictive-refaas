package domain

// TestingError represents a failure during the test execution and carries an
// associated error code.
type TestingError struct {
	err       error
	errorCode int
}

// NewTestingError returns a TestingError with the given error and code.
func NewTestingError(err error, code int) TestingError {
	return TestingError{err: err, errorCode: code}
}

func (e TestingError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

// Code returns the error code for the testing error.
func (e TestingError) Code() int {
	return e.errorCode
}

// CompilationError wraps errors produced during compilation.
type CompilationError struct {
	err error
}

// NewCompilationError returns a CompilationError wrapping err.
func NewCompilationError(err error) CompilationError {
	return CompilationError{err: err}
}

func (e CompilationError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

// LLMError wraps errors coming from LLM invocation or parsing.
type LLMError struct {
	err error
}

// NewLLMError returns a LLMError wrapping err.
func NewLLMError(err error) LLMError {
	return LLMError{err: err}
}

func (e LLMError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}
