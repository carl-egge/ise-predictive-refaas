package inputhandler

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// Reader defines the interface for reading deployment packages.
type Reader interface {
	ReadFromFile(sourceFile string) (*domain.DeploymentPackage, error)
	ReadFromReader(reader io.ReaderAt, size int64) (*domain.DeploymentPackage, error)
	ReadFromBytes(data []byte) (*domain.DeploymentPackage, error)
}

// ZipReader reads deployment packages from zip archives.
type ZipReader struct{}

// ReadFromFile reads a zip archive from sourceFile and converts it into a
// DeploymentPackage.
func (ZipReader) ReadFromFile(sourceFile string) (*domain.DeploymentPackage, error) {
	fs, err := os.OpenFile(sourceFile, os.O_RDONLY, 0666)
	if err != nil {
		return nil, err
	}
	stat, err := fs.Stat()
	if err != nil {
		return nil, err
	}
	defer fs.Close()
	return ReadFromReader(fs, stat.Size())
}

// ReadFromReader reads a zip archive from an io.ReaderAt and size and produces
// a DeploymentPackage.
func (ZipReader) ReadFromReader(reader io.ReaderAt, size int64) (*domain.DeploymentPackage, error) {
	return ReadFromReader(reader, size)
}

// ReadFromBytes reads a deployment package from a zip-encoded byte slice.
func (ZipReader) ReadFromBytes(data []byte) (*domain.DeploymentPackage, error) {
	reader := bytes.NewReader(data)
	return ReadFromReader(reader, int64(len(data)))
}

// ReadFromReader reads a zip archive from reader and size and produces a
// DeploymentPackage.
func ReadFromReader(reader io.ReaderAt, size int64) (*domain.DeploymentPackage, error) {
	dp := domain.DeploymentPackage{
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
	return &dp, nil
}

// ReadFromFile reads a zip archive from sourceFile and converts it into a
// DeploymentPackage.
func ReadFromFile(sourceFile string) (*domain.DeploymentPackage, error) {
	return ZipReader{}.ReadFromFile(sourceFile)
}

// ReadFromBytes reads a deployment package from a zip-encoded byte slice.
func ReadFromBytes(data []byte) (*domain.DeploymentPackage, error) {
	return ZipReader{}.ReadFromBytes(data)
}
