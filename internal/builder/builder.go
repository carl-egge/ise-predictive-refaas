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
	out, err := cc.runBuildCmds(code, dir, ctx)
	if err == nil {
		return out, nil
	}
	log.Debugf("failed to run build commands: %+v", err)
	if isGoModFailure(err) {
		return cc.rebuildWithFreshGoMod(code, dir, ctx)
	}
	if fixedOut, fixedErr, handled := cc.rebuildWithoutUnusedVars(code, dir, ctx, err); handled {
		return fixedOut, fixedErr
	}
	return out, err
}

// runBuildCmds prepares the build directory from the package and runs its
// build command list, stopping at the first failure.
func (cc *GolangBuilder) runBuildCmds(code *domain.DeploymentPackage, dir string, ctx context.Context) (string, error) {
	if err := cc.prepareBuildFolder(dir, code); err != nil {
		log.Debugf("failed to prepare build folder: %s", err.Error())
		return "", err
	}
	for _, cmd := range code.BuildCmd {
		out, err := cc.runBuildCommands(ctx, dir, cmd)
		if err != nil {
			return out, err
		}
	}
	return "", nil
}

// maxUnusedVarPasses bounds the strip/rebuild loop below. The compiler
// reports "declared and not used" a few at a time, so clearing one round
// commonly reveals the next; five rounds cleared every case observed in the
// 2026-08-30 run without letting a pathological file spin.
const maxUnusedVarPasses = 5

// rebuildWithoutUnusedVars applies the deterministic "declared and not used"
// repair (see unusedvars.go) and rebuilds, repeating while the compiler keeps
// naming more unused variables. It reports whether it handled the failure at
// all - when the error names none, or the source does not parse, the caller
// falls through to the LLM fixer unchanged.
//
// On a build that still fails for other reasons the rewritten source is kept
// rather than reverted: blanking a name preserves the right-hand side, so the
// rewrite cannot change behaviour, and the fixer is better off seeing the
// genuine remaining diagnostics than the same list with mechanical noise on
// top. The one rewrite that *can* break a build is blanking a name in a short
// declaration whose every other name was already declared, leaving ":=" with
// nothing new on the left; that error is recognised explicitly and reverts
// the whole attempt.
func (cc *GolangBuilder) rebuildWithoutUnusedVars(code *domain.DeploymentPackage, dir string, ctx context.Context, buildErr error) (string, error, bool) {
	original := code.RootFile
	current := buildErr
	applied := 0

	for pass := 0; pass < maxUnusedVarPasses; pass++ {
		vars := unusedVarDiagnostics(current.Error())
		if len(vars) == 0 {
			break
		}
		rewritten, changed := stripUnusedVars(code.RootFile, vars)
		if !changed {
			break
		}
		code.RootFile = rewritten
		applied += len(vars)
		log.Debugf("build: stripped %d unused variable(s) deterministically (pass %d), rebuilding", len(vars), pass+1)

		out, err := cc.rerunAfterRepair(code, dir, ctx)
		if err == nil {
			log.Debugf("build: unused-variable repair fixed the build after %d pass(es)", pass+1)
			return out, nil, true
		}
		if strings.Contains(err.Error(), "no new variables on left side of") {
			// Introduced by the rewrite itself - undo it entirely so the
			// fixer never sees a problem this repair created.
			log.Debugf("build: unused-variable repair would break the declaration, reverting")
			code.RootFile = original
			if perr := cc.prepareBuildFolder(dir, code); perr != nil {
				log.Debugf("build: failed to restore original source: %v", perr)
			}
			return "", nil, false
		}
		current = err
	}

	if applied == 0 {
		return "", nil, false
	}
	log.Debugf("build: stripped %d unused variable(s); build still fails for other reasons", applied)
	return "", current, true
}

// rerunAfterRepair re-runs the build command list after the source was
// rewritten. "go mod init" refuses to run against a directory that already
// has a go.mod, which it does whenever rebuildWithFreshGoMod has already
// swapped the command list - so the generated module files are cleared first
// and regenerated, exactly as that fallback does.
func (cc *GolangBuilder) rerunAfterRepair(code *domain.DeploymentPackage, dir string, ctx context.Context) (string, error) {
	for _, cmd := range code.BuildCmd {
		if strings.HasPrefix(strings.TrimSpace(cmd), "go mod init") {
			_ = os.Remove(filepath.Join(dir, "go.mod"))
			_ = os.Remove(filepath.Join(dir, "go.sum"))
			break
		}
	}
	return cc.runBuildCmds(code, dir, ctx)
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

// maxCompilerErrors caps how many *compiler* diagnostics reach the fixer
// prompt: Go compiler errors cascade, and later ones are usually consequences
// of the first, so a small focused list beats the full dump.
const maxCompilerErrors = 5

// maxModuleErrors caps module diagnostics separately, and higher. They do not
// cascade the way compiler errors do - `go mod tidy` reports one block per
// unresolvable import - and the block that names the offending package is the
// only line worth having, so crowding it out is the failure mode to avoid.
const maxModuleErrors = 10

// maxContinuationLines bounds how much of an indented explanation is folded
// into its diagnostic. Both the compiler ("A does not implement B (missing
// method M)") and the module resolver put the actual reason on these lines.
const maxContinuationLines = 4

// goDiagnosticRe matches Go compiler diagnostics like
// "./main.go:11:50: syntax error: ..." (column optional).
var goDiagnosticRe = regexp.MustCompile(`^(\./)?\S+\.go:\d+(:\d+)?:\s`)

// goProgressRe matches the "go: ..." lines that report *progress* rather than
// a problem. `go mod tidy` prints one "finding module for package" line per
// import and a "downloading"/"found" line per module resolved, whether or not
// the command ultimately succeeds.
//
// Filtering them is the point of [C13]. Before it, a tidy failure filled the
// entire five-line budget with these - the fixer for f16 was handed five
// "finding module for package <valid path>" lines, one per *correct* import,
// while the single bogus one (service/iotdata) never appeared. It then spent
// four attempts on a diagnostic in which nothing was actually wrong.
var goProgressRe = regexp.MustCompile(`^go: (finding module for package |downloading |found \S+ in |upgraded |extracting )`)

// extractDiagnostics pulls the individual compiler / module diagnostics out of
// raw build output, de-duplicated, progress-filtered and capped. It returns nil
// when the output contains no recognizable diagnostics (the caller then falls
// back to the raw output).
func extractDiagnostics(output string) []string {
	compiler, module := collectDiagnostics(output)
	out := make([]string, 0, len(compiler)+len(module)+2)
	out = appendCapped(out, compiler, maxCompilerErrors)
	out = appendCapped(out, module, maxModuleErrors)
	return out
}

// appendCapped appends at most limit diagnostics, noting how many were left
// out. The note is only added when something real was dropped: the old
// unconditional "further errors omitted" was itself misleading, since what had
// been omitted was usually progress chatter rather than errors.
func appendCapped(dst, diags []string, limit int) []string {
	if len(diags) <= limit {
		return append(dst, diags...)
	}
	dst = append(dst, diags[:limit]...)
	return append(dst, fmt.Sprintf("... and %d more errors omitted; fix the ones above first", len(diags)-limit))
}

// collectDiagnostics walks the build output once, splitting what it keeps into
// compiler diagnostics and module diagnostics so the two can be capped on their
// own terms.
//
// Indented lines are folded into the diagnostic above them rather than being
// dropped. This is not cosmetic: `go mod tidy` reports an unresolvable import
// as a two-line block whose *second* line is the only one that names the
// package and says why -
//
//	go: example.com imports
//		.../service/iotdata: module github.com/aws/aws-sdk-go-v2@latest found
//		(v1.45.1), but does not contain package .../service/iotdata
//
// and that line starts with a tab, not "go: ", so the previous prefix test
// discarded it no matter how high the cap was set.
func collectDiagnostics(output string) (compiler, module []string) {
	seen := make(map[string]bool)
	// where the diagnostic currently open for continuations lives, so an
	// indented line can be appended to the entry it belongs to
	var current *[]string
	continuations := 0

	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		if isContinuation(raw) {
			if current == nil || continuations >= maxContinuationLines {
				continue
			}
			list := *current
			list[len(list)-1] += " " + line
			continuations++
			continue
		}
		// a non-indented line ends the previous diagnostic's explanation
		current, continuations = nil, 0

		isModule := strings.HasPrefix(line, "go: ") || strings.HasPrefix(line, "go.mod:")
		if !goDiagnosticRe.MatchString(line) && !isModule {
			continue
		}
		if isModule && goProgressRe.MatchString(line) {
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true

		if isModule {
			module = append(module, line)
			current = &module
		} else {
			compiler = append(compiler, line)
			current = &compiler
		}
	}
	return compiler, module
}

// isContinuation reports whether a line is the indented explanation of the
// diagnostic above it. Blank lines are handled by the caller.
func isContinuation(raw string) bool {
	return strings.TrimSpace(raw) != "" && (strings.HasPrefix(raw, "\t") || strings.HasPrefix(raw, " "))
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
