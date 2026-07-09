package floci

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	log "github.com/sirupsen/logrus"
)

// lambdaMain is the custom-runtime entrypoint compiled alongside the translated
// code. The translated package is expected to expose a `handle` function with a
// Lambda-compatible signature (e.g. func(context.Context, json.RawMessage)
// (T, error)) — the same `handle` the goTester harness calls. We wrap it with
// aws-lambda-go so it speaks the Lambda Runtime API instead of stdin/stdout.
const lambdaMain = `package main

import "github.com/aws/aws-lambda-go/lambda"

func main() {
	lambda.Start(handle)
}
`

// packageLambda builds the translated working package into a provided.al2
// custom-runtime ZIP: a Linux "bootstrap" binary at the archive root. It
// returns the ZIP bytes ready for CreateFunction/UpdateFunctionCode.
//
// The build happens in a throwaway temp dir that contains the translated source
// plus our lambda entrypoint, but deliberately NOT the goTester's stdin/stdout
// test_handler.go (which has its own main and would collide).
func packageLambda(ctx context.Context, pkg *domain.DeploymentPackage) ([]byte, error) {
	if pkg == nil || strings.TrimSpace(pkg.RootFile) == "" {
		return nil, fmt.Errorf("floci: working package has no root file to package")
	}

	dir, err := os.MkdirTemp("", "floci_lambda")
	if err != nil {
		return nil, fmt.Errorf("floci: creating build dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := writeLambdaSources(dir, pkg); err != nil {
		return nil, err
	}

	// Resolve modules (the translated code rarely declares aws-lambda-go, so we
	// (re)generate the module graph). Network access is required the first time
	// to fetch aws-lambda-go; subsequent builds use the module cache.
	for _, step := range buildSteps() {
		if out, err := runCmd(ctx, dir, step); err != nil {
			return nil, fmt.Errorf("floci: lambda build step %q failed: %s: %w", strings.Join(step, " "), out, err)
		}
	}

	bootstrap := filepath.Join(dir, "bootstrap")
	if _, err := os.Stat(bootstrap); err != nil {
		return nil, fmt.Errorf("floci: bootstrap binary not produced: %w", err)
	}

	zipped, err := zipBootstrap(bootstrap)
	if err != nil {
		return nil, err
	}
	log.Debugf("floci: packaged lambda bootstrap (%d bytes zip)", len(zipped))
	return zipped, nil
}

// writeLambdaSources lays the translated package and the lambda entrypoint into
// dir, skipping the stdin/stdout test harness so there is exactly one main().
func writeLambdaSources(dir string, pkg *domain.DeploymentPackage) error {
	write := func(name, content string) error {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return fmt.Errorf("floci: writing %s: %w", name, err)
		}
		return nil
	}

	if err := write("main.go", pkg.RootFile); err != nil {
		return err
	}
	for name, content := range pkg.BuildFiles {
		// test_handler.go provides the goTester's own main(); a go.mod here may
		// pin a different module path. We regenerate go.mod ourselves below.
		if name == "test_handler.go" || name == "go.mod" || name == "go.sum" {
			continue
		}
		if err := write(name, content); err != nil {
			return err
		}
	}
	return write("lambda_main.go", lambdaMain)
}

func buildSteps() [][]string {
	return [][]string{
		{"go", "mod", "init", "floci-lambda"},
		{"go", "mod", "tidy"},
		// provided.al2 custom runtime: static linux binary, lambda.norpc drops
		// the net/rpc transport that provided runtimes don't use.
		{"go", "build", "-tags", "lambda.norpc", "-o", "bootstrap", "."},
	}
}

func runCmd(ctx context.Context, dir string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOOS=linux",
		"GOARCH=amd64",
		"CGO_ENABLED=0",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// zipBootstrap writes the bootstrap binary into a ZIP at the archive root with
// an executable file mode, which Floci's Lambda extractor expects.
func zipBootstrap(bootstrapPath string) ([]byte, error) {
	data, err := os.ReadFile(bootstrapPath)
	if err != nil {
		return nil, fmt.Errorf("floci: reading bootstrap: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "bootstrap", Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return nil, fmt.Errorf("floci: zip header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return nil, fmt.Errorf("floci: writing zip entry: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("floci: closing zip: %w", err)
	}
	return buf.Bytes(), nil
}
