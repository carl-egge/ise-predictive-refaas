package builder

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	log "github.com/sirupsen/logrus"
)

//go:embed test_handler.txt
var goTestHandler string

// GolangBuilder writes a test harness and attempts to build the working
// package to ensure the converted code compiles.
type GolangBuilder struct {
	TestHandler string
}

func init() {
	pipeline.RegisterConverterFactory("goBuilder", NewGolangBuilder)
}

// NewGolangBuilder creates a GolangBuilder instance using an optional test
// handler override from args.
func NewGolangBuilder(args map[string]interface{}) pipeline.Converter {
	if handler, ok := args["handler"].(string); ok {
		return &GolangBuilder{TestHandler: handler}
	}
	return &GolangBuilder{TestHandler: goTestHandler}
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
		request.AddError(err)
		return err
	}
	runner.SetWorkingDir(dir)
	code := request.WorkingPackage
	code.BuildFiles["handler.go"] = string(cc.TestHandler)
	ctx := runner.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := cc.build(request, dir, ctx); err != nil {
		if request.Metrics != nil {
			request.Metrics.BuildError += 1
		}
		log.Debugf("failed to build: %s", err.Error())
		request.AddError(err)
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
			if strings.Contains(err.Error(), " unknown revision") {
				delete(code.BuildFiles, "go.mod")
				code.BuildCmd = []string{
					"go mod init example.com",
					"go mod tidy",
					"go build -o fn .",
				}
				out, err := cc.runBuildCommands(ctx, dir, cmd)
				return out, err
			}
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
	cmds := strings.Split(buildCmd, " ")

	cmd := exec.CommandContext(ctx, cmds[0], cmds[1:]...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("failed to build. %s \n\n %+v", stdout.String(), err)
	}
	return stdout.String(), nil
}
