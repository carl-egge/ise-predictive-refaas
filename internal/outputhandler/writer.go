package outputhandler

import (
	"archive/zip"
	"io"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	log "github.com/sirupsen/logrus"
)

// Writer defines the interface for writing deployment packages.
type Writer interface {
	WritePackage(writer io.Writer, dp *domain.DeploymentPackage) error
}

// ZipWriter writes deployment packages as zip archives.
type ZipWriter struct{}

// WritePackage writes the given DeploymentPackage into the supplied writer as a
// zip archive.
func (ZipWriter) WritePackage(writer io.Writer, dp *domain.DeploymentPackage) error {
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

	if err := writeFile("main.go", dp.RootFile); err != nil {
		return err
	}
	for name, file := range dp.TestFiles {
		if err := writeFile(name, file); err != nil {
			return err
		}
	}

	for name, file := range dp.BuildFiles {
		if err := writeFile(name, file); err != nil {
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

		if err := writeFile("build.sh", builder.String()); err != nil {
			return err
		}
	}
	return zw.Close()
}

// WritePackage writes dp as a zip archive using the default ZipWriter.
func WritePackage(writer io.Writer, dp *domain.DeploymentPackage) error {
	return ZipWriter{}.WritePackage(writer, dp)
}
