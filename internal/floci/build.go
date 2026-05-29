package floci

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// BuildLambdaZip builds a Go bootstrap binary and returns a Lambda-ready ZIP.
func BuildLambdaZip(ctx context.Context, pkg *domain.DeploymentPackage, cfg Config) ([]byte, error) {
	if pkg == nil {
		return nil, fmt.Errorf("missing deployment package")
	}

	buildDir, err := os.MkdirTemp("", "floci-lambda-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(buildDir)

	if err := writePackageSources(buildDir, pkg); err != nil {
		return nil, err
	}

	if err := ensureGoModule(ctx, buildDir, pkg); err != nil {
		return nil, err
	}

	if err := buildBootstrap(ctx, buildDir, cfg); err != nil {
		return nil, err
	}

	zipBytes, err := zipBootstrap(buildDir)
	if err != nil {
		return nil, err
	}

	return zipBytes, nil
}

func writePackageSources(dir string, pkg *domain.DeploymentPackage) error {
	if err := writeFile(dir, "main.go", pkg.RootFile); err != nil {
		return err
	}
	for name, file := range pkg.BuildFiles {
		if err := writeFile(dir, name, file); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(dir, name, content string) error {
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func ensureGoModule(ctx context.Context, dir string, pkg *domain.DeploymentPackage) error {
	if _, ok := pkg.BuildFiles["go.mod"]; !ok {
		if err := runCommand(ctx, dir, nil, "go", "mod", "init", "example.com/translated"); err != nil {
			return err
		}
	}
	if err := runCommand(ctx, dir, nil, "go", "mod", "tidy"); err != nil {
		return err
	}
	return nil
}

func buildBootstrap(ctx context.Context, dir string, cfg Config) error {
	env := append(os.Environ(),
		"GOOS="+cfg.GoOS,
		"GOARCH="+cfg.GoArch,
		"CGO_ENABLED=0",
	)
	args := []string{"build", "-tags", "lambda.norpc", "-o", "bootstrap", "."}
	if err := runCommand(ctx, dir, env, "go", args...); err != nil {
		return err
	}
	return os.Chmod(filepath.Join(dir, "bootstrap"), 0755)
}

func runCommand(ctx context.Context, dir string, env []string, cmd string, args ...string) error {
	command := exec.CommandContext(ctx, cmd, args...)
	command.Dir = dir
	if env != nil {
		command.Env = env
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command failed: %s %s: %s", cmd, strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return nil
}

func zipBootstrap(dir string) ([]byte, error) {
	bootstrapPath := filepath.Join(dir, "bootstrap")
	file, err := os.Open(bootstrapPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)
	defer zipWriter.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return nil, err
	}
	header.Name = "bootstrap"
	header.Method = zip.Deflate
	header.SetMode(0755)

	entry, err := zipWriter.CreateHeader(header)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(entry, file); err != nil {
		return nil, err
	}
	if err := zipWriter.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
