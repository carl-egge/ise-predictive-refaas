// Package main provides utilities for reading and writing deployment
// packages from zip streams used by the conversion service.
package main

import (
	"archive/zip"
	"io"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
)

// ReadDeploymentPackageFromFile reads a zip archive from `sourceFile`
// and converts it into a `DeploymentPackage`.
func (cc *PipelineRunner) ReadDeploymentPackageFromFile(sourceFile string) (*DeploymentPackage, error) {
	fs, err := os.OpenFile(sourceFile, os.O_RDONLY, 0666)
	if err != nil {
		return nil, err
	}
	stat, err := fs.Stat()
	if err != nil {
		return nil, err
	}
	defer fs.Close()
	dp, err := cc.ReadDeploymentPackageFromReader(fs, stat.Size())

	if err != nil {
		return nil, err
	}
	return dp, nil
}

// ReadDeploymentPackageFromReader reads a zip archive from an
// `io.ReaderAt` and size and produces a `DeploymentPackage`.
func (cc *PipelineRunner) ReadDeploymentPackageFromReader(reader io.ReaderAt, size int64) (*DeploymentPackage, error) {
	dp := DeploymentPackage{
		RootFile:   "",
		TestFiles:  make(map[string]string),
		BuildFiles: make(map[string]string),
		BuildCmd:   make([]string, 0),
		Env:        make([]string, 0),
	}
	zipfs, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, err
	}
	for _, file := range zipfs.File {
		//TODO: fix me
		if strings.HasSuffix(file.Name, ".py") || strings.HasSuffix(file.Name, ".go") {
			fileReader, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer fileReader.Close()
			rootFile, err := io.ReadAll(fileReader)
			if err != nil {
				return nil, err
			}
			dp.RootFile = string(rootFile)
		} else if strings.HasPrefix(file.Name, "test/") {
			if file.FileInfo().IsDir() {
				continue
			}
			fileReader, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer fileReader.Close()
			testFile, err := io.ReadAll(fileReader)
			if err != nil {
				return nil, err
			}
			dp.TestFiles[file.Name] = string(testFile)
		} else if strings.HasSuffix(file.Name, ".env") {
			fileReader, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer fileReader.Close()
			envFile, err := io.ReadAll(fileReader)
			if err != nil {
				return nil, err
			}
			dp.Env = append(dp.Env, strings.Split(string(envFile), "\n")...)
		}
	}
	return &dp, err
}

// WriteDeploymentPackage writes the given `DeploymentPackage` into the
// supplied writer as a zip archive.
func (cc *PipelineRunner) WriteDeploymentPackage(writer io.Writer, dp *DeploymentPackage) error {
	zw := zip.NewWriter(writer)

	writeFile := func(file string, content string) error {
		fp, err := zw.Create(file)
		if err != nil {
			return err
		}
		written, err := fp.Write([]byte(content))
		if err != nil {
			log.Debugf("Failed to write to %s: %s", file, err)
			return err
		}
		log.Debugf("Written %s [%d] bytes", file, written)
		return nil
	}

	err := writeFile("main.go", dp.RootFile)
	if err != nil {
		return err
	}
	for name, file := range dp.TestFiles {
		err := writeFile(name, file)
		if err != nil {
			return err
		}
	}

	for name, file := range dp.BuildFiles {
		err := writeFile(name, file)
		if err != nil {
			return err
		}
	}

	if len(dp.BuildCmd) > 0 {
		var builder strings.Builder
		builder.WriteString("#! /bin/sh\n\n")
		for _, line := range dp.BuildCmd {
			builder.WriteString(line)
			builder.WriteString("\n")
		}

		err := writeFile("build.sh", builder.String())
		if err != nil {
			return err
		}
	}
	err = zw.Close()
	return err
}
