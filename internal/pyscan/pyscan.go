// Package pyscan performs deterministic, LLM-free static analysis of a
// Python serverless function.
//
// It has two consumers and exists once for both ([C8] and [I3]):
//
//   - the translate prompt, which receives library-mapping and construct
//     hints so the model does not have to rediscover that requests maps to
//     net/http on every call, and
//   - the prediction module, which needs a fixed-width numeric feature
//     vector describing the same source *before* any LLM call is made.
//
// Building these separately would let the hints and the model features drift
// apart, so both are derived from one parse. The parse itself runs in
// CPython (extract.py, embedded below and executed through the interpreter
// on PATH), because Go has no Python parser and an approximate tokenizer
// would put the training-time and serving-time feature values on different
// bases - the classic way a model ends up working offline and not in
// production.
//
// Cost: one process spawn per scan, tens of milliseconds. That is
// negligible against a single LLM call (~10^-6 of it, quantified in [I8]),
// which is what makes prediction affordable at all.
package pyscan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:generate true

// SchemaVersion is the contract version between extract.py and this package.
// Bump both together; Scan rejects a mismatch rather than silently reading a
// differently-shaped object.
const SchemaVersion = 3

// DefaultTimeout bounds one scan. Parsing is linear and fast, so this only
// ever fires on a pathological input or a wedged interpreter; without it a
// single upload could stall the (single) conversion worker indefinitely.
const DefaultTimeout = 20 * time.Second

var (
	// ErrUnavailable means no usable Python interpreter was found. Callers
	// that merely enrich a prompt should degrade; callers that need features
	// to make a decision must not.
	ErrUnavailable = errors.New("pyscan: no python3 interpreter available")

	// ErrParse means the interpreter ran but the source is not valid Python.
	ErrParse = errors.New("pyscan: source is not parseable Python")
)

// Result is the raw fact set extract.py reports. Policy - which third-party
// import maps to which Go package, which are infeasible, the feature vector's
// key order - is applied on top of this by libmap.go and features.go, never
// inside the parser.
type Result struct {
	SchemaVersion int                `json:"schema_version"`
	Metrics       map[string]float64 `json:"metrics"`
	Imports       []string           `json:"imports"`
	ThirdParty    []string           `json:"third_party_imports"`
	Stdlib        []string           `json:"stdlib_imports"`
	Boto3Services []string           `json:"boto3_services"`
	// ClientFactoryLiterals are string literals passed as the first argument
	// to any client/resource factory call, including a project's own wrapper
	// around boto3. It is a looser net than Boto3Services and is kept separate
	// on purpose: Boto3Services feeds the `n_boto3_services` feature column,
	// so widening it would change the values the shipped prediction model was
	// fitted on. This field feeds prompt hints only.
	ClientFactoryLiterals []string       `json:"client_factory_literals"`
	DynamicCalls          map[string]int `json:"dynamic_calls"`
	TopLevelFuncs         []string       `json:"top_level_functions"`
	// CodeLineHashes is the AST-canonicalised structural fingerprint used by
	// the near-duplicate audit ([I11]). Hashes, not lines: the fingerprint
	// travels in the feature table and must not carry a copy of the source.
	CodeLineHashes []string `json:"code_line_hashes,omitempty"`
}

// Metric returns a numeric metric by name, or 0 when absent. Absence is not
// an error: a metric added to extract.py after a model was trained must not
// break scoring, it must read as zero.
func (r *Result) Metric(name string) float64 {
	if r == nil {
		return 0
	}
	return r.Metrics[name]
}

// scriptOnce caches the unpacked extract.py path and the resolved
// interpreter. Both are process-wide and immutable once resolved, so the
// per-scan cost is one exec, not a filesystem write plus a PATH search.
var (
	scriptOnce sync.Once
	scriptPath string
	scriptErr  error

	pythonOnce sync.Once
	pythonPath string
	pythonErr  error
)

// candidateInterpreters is searched in order. PYSCAN_PYTHON wins outright so
// a deployment can pin an interpreter that is not on PATH.
var candidateInterpreters = []string{"python3", "python"}

func interpreter() (string, error) {
	pythonOnce.Do(func() {
		if pinned := strings.TrimSpace(os.Getenv("PYSCAN_PYTHON")); pinned != "" {
			if path, err := exec.LookPath(pinned); err == nil {
				pythonPath = path
				return
			}
			pythonErr = fmt.Errorf("%w: PYSCAN_PYTHON=%q is not executable", ErrUnavailable, pinned)
			return
		}
		for _, name := range candidateInterpreters {
			if path, err := exec.LookPath(name); err == nil {
				pythonPath = path
				return
			}
		}
		pythonErr = fmt.Errorf("%w (looked for %s on PATH; set PYSCAN_PYTHON to override)",
			ErrUnavailable, strings.Join(candidateInterpreters, ", "))
	})
	return pythonPath, pythonErr
}

// script materializes the embedded analyzer next to the process's temp dir.
// It is written once per process rather than per scan: the content is fixed
// at build time, so re-writing it on every upload would be pure syscall
// overhead on the conversion worker's hot path.
func script() (string, error) {
	scriptOnce.Do(func() {
		dir, err := os.MkdirTemp("", "pyscan-")
		if err != nil {
			scriptErr = fmt.Errorf("pyscan: cannot create scratch dir: %w", err)
			return
		}
		path := filepath.Join(dir, "extract.py")
		if err := os.WriteFile(path, extractPy, 0o600); err != nil {
			scriptErr = fmt.Errorf("pyscan: cannot write analyzer: %w", err)
			return
		}
		scriptPath = path
	})
	return scriptPath, scriptErr
}

// Available reports whether a scan can run at all, without running one. Lets
// a caller decide up front between degrading and failing.
func Available() bool {
	_, err := interpreter()
	return err == nil
}

// Scan analyzes one Python source file.
//
// The source is fed on stdin rather than written to disk: it keeps the
// uploaded function out of the filesystem and removes any question of
// cleanup on the error paths.
func Scan(ctx context.Context, source string) (*Result, error) {
	if strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("pyscan: empty source")
	}

	python, err := interpreter()
	if err != nil {
		return nil, err
	}
	path, err := script()
	if err != nil {
		return nil, err
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, python, path)
	cmd.Stdin = strings.NewReader(source)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// The analyzer imports nothing outside the standard library; an
	// inherited PYTHONPATH could shadow `ast` or `json` and is stripped.
	cmd.Env = scrubbedEnv()

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("pyscan: analysis aborted: %w", ctxErr)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return nil, fmt.Errorf("%w: %s", ErrParse, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("pyscan: analyzer failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	var result Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("pyscan: analyzer emitted unreadable output: %w", err)
	}
	if result.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("pyscan: analyzer schema %d, expected %d (extract.py and pyscan.go are out of sync)",
			result.SchemaVersion, SchemaVersion)
	}
	return &result, nil
}

// scrubbedEnv drops the Python module-resolution variables so the analyzer
// always runs against the interpreter's own standard library.
func scrubbedEnv() []string {
	const prefix = "PYTHON"
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, prefix) {
			continue
		}
		out = append(out, kv)
	}
	// Keeps __pycache__ out of the scratch dir and shaves a little startup.
	return append(out, "PYTHONDONTWRITEBYTECODE=1")
}
