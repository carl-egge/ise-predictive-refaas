// Package main contains build helpers used to compile and test the
// converted Go code inside a temporary working directory.
package main

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

	log "github.com/sirupsen/logrus"
)

//go:embed test_handler.txt
var goTestHandler string

// GolangBuilder writes a test harness and attempts to build the working
// package to ensure the converted code compiles.
type GolangBuilder struct {
	TestHandler string
}

// makeGolangBuilder creates a `GolangBuilder` instance using an
// optional test handler override from `args`.
func makeGolangBuilder(args map[string]interface{}) Converter {
	if handler, ok := args["handler"].(string); ok {
		return &GolangBuilder{TestHandler: handler}
	} else {
		return &GolangBuilder{TestHandler: goTestHandler}
	}
}

// Apply attempts to compile the `request.WorkingPackage` in a temporary
// directory and records build timing/errors into `request.Metrics`.
func (cc *GolangBuilder) Apply(runner *PipelineRunner, request *ConversionRequest) error {
	//let's keep this clean
	if runner.WorkingDir != "" {
		defer os.RemoveAll(runner.WorkingDir)
	}
	start := time.Now()
	defer func() {
		request.Metrics.BuildTime = time.Since(start)
	}()
	dir, err := os.MkdirTemp("", "fn_lmm")
	if err != nil {
		log.Errorf("Error creating temporary directory: %s", err)
		request.err = append(request.err, err)
		return err
	}
	runner.WorkingDir = dir
	code := request.WorkingPackage
	code.BuildFiles["handler.go"] = string(cc.TestHandler)
	//Build testable version
	err = cc.build(request, dir)

	if err != nil {
		request.Metrics.BuildError += 1
		log.Debugf("failed to build: %s", err.Error())
		request.err = append(request.err, err)
		return CompilationError{err}
	}
	log.Debugf("compiled code in %s", time.Since(start))

	return nil
}

func (cc *GolangBuilder) build(requests *ConversionRequest, dir string) error {
	code := requests.WorkingPackage

	_, err := cc.doBuild(code, dir)
	if err != nil {
		log.Debugf("failed to build")
		return err
	}

	return nil
}

func (cc *GolangBuilder) doBuild(code *DeploymentPackage, dir string) (string, error) {
	err := cc.prepareBuildFolder(dir, code)
	if err != nil {
		log.Debugf("failed to prepare build folder: %s", err.Error())
		return "", err
	}
	ctx := context.Background()
	for _, cmd := range code.BuildCmd {
		out, err := cc.runBuildCommands(ctx, dir, cmd)
		if err != nil {
			log.Debugf("failed to run build commands: %+v", err)
			if strings.Contains(err.Error(), " unknown revision") {
				//atempt to remove go.mod to fix the issue
				delete(code.BuildFiles, "go.mod")
				code.BuildCmd = []string{
					"go mod init example.com",
					"go mod tidy",
					"go build -o fn .",
				}
				out, err := cc.runBuildCommands(ctx, dir, cmd)
				return out, err
			} else {
				return out, err
			}

		}
	}
	return "", nil
}

func (cc *GolangBuilder) prepareBuildFolder(dir string, code *DeploymentPackage) error {
	writeToDir := func(fname, code string) error {
		fpath := filepath.Join(dir, fname)
		if _, err := os.Stat(fpath); err == nil {
			err := os.Remove(fpath)
			if err != nil {
				return err
			}
		}

		fs, err := os.OpenFile(fpath, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", fname, err)
		}
		defer fs.Close()
		_, err = fs.Write([]byte(code))
		if err != nil {
			return fmt.Errorf("failed to write file %s: %w", fname, err)
		}
		return nil
	}
	err := writeToDir("main.go", code.RootFile)
	if err != nil {
		return err
	}
	for fname, file := range code.BuildFiles {
		err = writeToDir(fname, file)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cc *GolangBuilder) runBuildCommands(ctx context.Context, dir, build_cmd string) (string, error) {
	cmds := strings.Split(build_cmd, " ")

	cmd := exec.CommandContext(ctx, cmds[0], cmds[1:]...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	err := cmd.Run()

	if err != nil {
		return stdout.String(), fmt.Errorf("failed to build. %s \n\n %+v", stdout.String(), err)
	}
	return stdout.String(), nil
}
