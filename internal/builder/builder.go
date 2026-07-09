package builder

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	log "github.com/sirupsen/logrus"
)

//go:embed test_handler.txt
var goTestHandler string

// defaultBuildTimeout bounds a single build command; `go mod tidy` may
// download modules, so it is generous but no longer unbounded.
const defaultBuildTimeout = 2 * time.Minute

// GolangBuilder writes a test harness and attempts to build the working
// package to ensure the converted code compiles.
type GolangBuilder struct {
	TestHandler string
	// buildTimeout bounds each individual build command (task_args
	// "build_timeout", default defaultBuildTimeout).
	buildTimeout time.Duration
}

func init() {
	pipeline.RegisterConverterFactory("goBuilder", NewGolangBuilder)
}

// NewGolangBuilder creates a GolangBuilder instance using an optional test
// handler override and build timeout from args.
func NewGolangBuilder(args map[string]interface{}) pipeline.Converter {
	builder := &GolangBuilder{
		TestHandler:  goTestHandler,
		buildTimeout: parseTimeout(args, "build_timeout", defaultBuildTimeout),
	}
	if handler, ok := args["handler"].(string); ok {
		builder.TestHandler = handler
	}
	return builder
}

// parseTimeout reads a duration task param (a string like "30s"/"2m", or a
// bare number of seconds from a JSON config), falling back to def.
func parseTimeout(args map[string]interface{}, key string, def time.Duration) time.Duration {
	switch v := args[key].(type) {
	case string:
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		log.Warnf("ignoring invalid %s %q", key, v)
	case float64:
		if v > 0 {
			return time.Duration(v * float64(time.Second))
		}
	case int:
		if v > 0 {
			return time.Duration(v) * time.Second
		}
	}
	return def
}

// Apply attempts to compile the request.WorkingPackage in a temporary directory
// and records build timing/errors into request.Metrics.
func (cc *GolangBuilder) Apply(runner *pipeline.Runner, request *domain.ConversionRequest) error {
	if runner.WorkingDir() != "" {
		defer os.RemoveAll(runner.WorkingDir())
	}
	start := time.Now()
	defer func() {
		if request.Metrics != nil {
			request.Metrics.BuildTime = time.Since(start)
		}
	}()

	dir, err := os.MkdirTemp("", "fn_lmm")
	if err != nil {
		log.Errorf("Error creating temporary directory: %s", err)
		return err
	}
	runner.SetWorkingDir(dir)
	code := request.WorkingPackage
	code.BuildFiles["test_handler.go"] = string(cc.TestHandler)
	if err := cc.build(request, dir, runner); err != nil {
		if request.Metrics != nil {
			request.Metrics.BuildError += 1
		}
		log.Debugf("failed to build: %s", err.Error())
		// Don't AddError here too - executeTask (internal/pipeline/pipeline.go)
		// already records every returned task error into req.errs exactly
		// once; doing it here as well duplicated the same failure (once raw,
		// once CompilationError-wrapped) in the error history and in the
		// {{ .failures }}/issues lists derived from it.
		return domain.NewCompilationError(err)
	}
	log.Debugf("compiled code in %s", time.Since(start))

	return nil
}

func (cc *GolangBuilder) build(requests *domain.ConversionRequest, dir string, ctx context.Context) error {
	code := requests.WorkingPackage

	_, err := cc.doBuild(code, dir, ctx)
	if err != nil {
		log.Debugf("failed to build")
		return err
	}

	return nil
}

func (cc *GolangBuilder) doBuild(code *domain.DeploymentPackage, dir string, ctx context.Context) (string, error) {
	if err := cc.prepareBuildFolder(dir, code); err != nil {
		log.Debugf("failed to prepare build folder: %s", err.Error())
		return "", err
	}
	for _, cmd := range code.BuildCmd {
		out, err := cc.runBuildCommands(ctx, dir, cmd)
		if err != nil {
			log.Debugf("failed to run build commands: %+v", err)
			if isGoModFailure(err) {
				return cc.rebuildWithFreshGoMod(code, dir, ctx)
			}
			return out, err
		}
	}
	return "", nil
}

// isGoModFailure reports whether a build error points at a broken or
// unresolvable go.mod (typically LLM-generated), covering the failure texts
// observed in real runs (see examples/metrics), not just "unknown revision".
func isGoModFailure(err error) bool {
	msg := err.Error()
	for _, marker := range []string{
		"unknown revision",
		"errors parsing go.mod",
		"invalid version",
		"unknown directive",
		"missing go.sum entry",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// rebuildWithFreshGoMod discards the broken go.mod/go.sum both from the
// package and from the build dir on disk (deleting only the BuildFiles entry
// would leave the bad file in place), then runs the full regenerate-and-build
// command list instead of re-running the command that just failed.
func (cc *GolangBuilder) rebuildWithFreshGoMod(code *domain.DeploymentPackage, dir string, ctx context.Context) (string, error) {
	delete(code.BuildFiles, "go.mod")
	delete(code.BuildFiles, "go.sum")
	_ = os.Remove(filepath.Join(dir, "go.mod"))
	_ = os.Remove(filepath.Join(dir, "go.sum"))
	code.BuildCmd = []string{
		"go mod init example.com",
		"go mod tidy",
		"go build -o fn .",
	}
	for _, cmd := range code.BuildCmd {
		if out, err := cc.runBuildCommands(ctx, dir, cmd); err != nil {
			return out, err
		}
	}
	return "", nil
}

func (cc *GolangBuilder) prepareBuildFolder(dir string, code *domain.DeploymentPackage) error {
	writeToDir := func(fname, code string) error {
		fpath := filepath.Join(dir, fname)
		if _, err := os.Stat(fpath); err == nil {
			if err := os.Remove(fpath); err != nil {
				return err
			}
		}

		fs, err := os.OpenFile(fpath, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", fname, err)
		}
		defer fs.Close()
		if _, err := fs.Write([]byte(code)); err != nil {
			return fmt.Errorf("failed to write file %s: %w", fname, err)
		}
		return nil
	}

	if err := writeToDir("main.go", code.RootFile); err != nil {
		return err
	}
	for fname, file := range code.BuildFiles {
		if err := writeToDir(fname, file); err != nil {
			return err
		}
	}
	return nil
}

func (cc *GolangBuilder) runBuildCommands(ctx context.Context, dir, buildCmd string) (string, error) {
	timeout := cc.buildTimeout
	if timeout <= 0 {
		timeout = defaultBuildTimeout
	}
	// A per-command timeout: without it a hanging build (e.g. a module
	// download against an unreachable proxy) stalls the single worker until
	// someone manually stops the job.
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmds := strings.Split(buildCmd, " ")

	cmd := exec.CommandContext(cmdCtx, cmds[0], cmds[1:]...)
	// force Run to return shortly after the timeout even if a child process
	// keeps the output pipes open (see the equivalent note in validator.go)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	if err := cmd.Run(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return stdout.String(), fmt.Errorf("build command %q timed out after %s. %s", buildCmd, timeout, stdout.String())
		}
		return stdout.String(), formatBuildError(buildCmd, stdout.String(), err)
	}
	return stdout.String(), nil
}

// maxCompilerErrors caps how many diagnostics reach the fixer prompt: Go
// compiler errors cascade, and later ones are usually consequences of the
// first, so a small focused list beats the full dump.
const maxCompilerErrors = 5

// goDiagnosticRe matches Go compiler diagnostics like
// "./main.go:11:50: syntax error: ..." (column optional).
var goDiagnosticRe = regexp.MustCompile(`^(\./)?\S+\.go:\d+(:\d+)?:\s`)

// extractDiagnostics pulls the individual compiler / module diagnostics out
// of raw build output, de-duplicated and capped at maxCompilerErrors. It
// returns nil when the output contains no recognizable diagnostics (the
// caller then falls back to the raw output).
func extractDiagnostics(output string) []string {
	seen := make(map[string]bool)
	diags := make([]string, 0, maxCompilerErrors)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		// compiler diagnostics, module errors ("go: ...") and go.mod parse
		// errors ("go.mod:6: unknown directive ...")
		if !goDiagnosticRe.MatchString(line) && !strings.HasPrefix(line, "go: ") && !strings.HasPrefix(line, "go.mod:") {
			continue
		}
		seen[line] = true
		if len(diags) >= maxCompilerErrors {
			diags = append(diags, "... further errors omitted; fix the ones above first")
			break
		}
		diags = append(diags, line)
	}
	return diags
}

// formatBuildError turns raw build output into the structured error the
// fixer prompt receives via {{ .issue }}: a numbered list of distinct
// diagnostics instead of a raw log dump. When nothing parseable is found it
// preserves the previous raw format. The diagnostic lines are kept verbatim,
// so the go.mod failure markers isGoModFailure looks for stay detectable.
func formatBuildError(buildCmd, output string, err error) error {
	diags := extractDiagnostics(output)
	if len(diags) == 0 {
		return fmt.Errorf("failed to build. %s%s", output, exitStatusSuffix(err))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "the build command %q failed with the following errors:\n", buildCmd)
	for i, d := range diags {
		fmt.Fprintf(&b, "%d. %s\n", i+1, d)
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

// exitStatusSuffix formats err for appending to the captured build output,
// unless err is just the command's exit status (e.g. "exit status 1") - that
// adds no information once the command's combined stdout/stderr is already
// shown, and only clutters the {{ .issue }} text the fixer prompt reads.
func exitStatusSuffix(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return ""
	}
	return fmt.Sprintf("\n\n %+v", err)
}
