package domain

// TestFailure kinds. The values are human-readable on purpose - they appear
// verbatim in error summaries and in the {{ .failures }} evidence block that
// repair/align prompts consume.
const (
	// TestFailureMismatch: the program ran but produced the wrong output.
	TestFailureMismatch = "output mismatch"
	// TestFailureError: the program crashed / exited non-zero.
	TestFailureError = "execution error"
	// TestFailureFixture: the test fixture itself could not be parsed.
	TestFailureFixture = "invalid test fixture"
	// TestFailureTimeout: the program exceeded the per-test time limit
	// (typically an infinite loop or a blocking call without a timeout).
	TestFailureTimeout = "test timeout"
	// TestFailureSetup: a declarative setup action could not be applied, so
	// the case never ran. An infrastructure problem, not a translation
	// defect - it must not be counted as one.
	TestFailureSetup = "setup failed"
	// TestFailureSideEffect: the function responded acceptably but did not
	// leave the AWS state the original produced. Only the Floci route can
	// observe this.
	TestFailureSideEffect = "side-effect mismatch"
)

// TestFailure captures the evidence of one failing test case in the form a
// repair prompt needs: which input produced which actual output versus what
// was expected. Fields are truncated at capture time so the evidence stays
// prompt-sized.
type TestFailure struct {
	Name     string
	Kind     string // one of the TestFailure* constants above
	Input    string
	Expected string
	Actual   string
	Stderr   string
	// Detail is the validator's explanation of the mismatch (e.g. the JSON
	// path of the first divergence), when it can produce one.
	Detail string
}

// TestingError represents a failure during the test execution and carries an
// associated error code plus, optionally, per-test failure evidence.
type TestingError struct {
	err       error
	errorCode int
	failures  []TestFailure
}

// NewTestingError returns a TestingError with the given error and code.
func NewTestingError(err error, code int) TestingError {
	return TestingError{err: err, errorCode: code}
}

// NewTestingErrorWithFailures returns a TestingError carrying the per-test
// failure evidence; the error code is the number of failed tests.
func NewTestingErrorWithFailures(err error, failures []TestFailure) TestingError {
	return TestingError{err: err, errorCode: len(failures), failures: failures}
}

// Failures returns the per-test failure evidence, if the producing stage
// collected any.
func (e TestingError) Failures() []TestFailure {
	return e.failures
}

func (e TestingError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

// Unwrap exposes the wrapped error so errors.Is/errors.As can see through a
// TestingError to whatever it wraps (e.g. a context deadline error), and so
// TestingError values compose correctly inside errors.Join/fmt.Errorf("%w").
func (e TestingError) Unwrap() error {
	return e.err
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

// Unwrap exposes the wrapped error - see TestingError.Unwrap.
func (e CompilationError) Unwrap() error {
	return e.err
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

// Unwrap exposes the wrapped error - see TestingError.Unwrap.
func (e LLMError) Unwrap() error {
	return e.err
}
