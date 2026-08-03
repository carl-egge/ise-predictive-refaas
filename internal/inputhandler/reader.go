package inputhandler

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
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
	rootNames := make([]string, 0, 1)
	for _, file := range zipfs.File {
		// macOS zips add AppleDouble resource-fork copies of every entry;
		// "__MACOSX/._main.py" would otherwise pass the suffix check below
		// and clobber the real source with resource-fork bytes.
		if strings.HasPrefix(file.Name, "__MACOSX/") || strings.HasPrefix(path.Base(file.Name), "._") {
			continue
		}
		if strings.HasSuffix(file.Name, ".py") || strings.HasSuffix(file.Name, ".go") {
			rootNames = append(rootNames, file.Name)
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
			if strings.HasSuffix(file.Name, ".py") {
				dp.Suffix = "py"
			} else {
				dp.Suffix = "go"
			}
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
		} else if path.Base(file.Name) == domain.MetaFileName {
			// The dataset ships per-function metadata next to main.py; it is
			// the only signal that says which dataset element this artifact
			// is. Checked after the test/ branch so a test/meta.json stays a
			// fixture.
			fileReader, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer fileReader.Close()
			metaFile, err := io.ReadAll(fileReader)
			if err != nil {
				return nil, err
			}
			meta, err := domain.ParseFunctionMeta(metaFile)
			if err != nil {
				return nil, fmt.Errorf("invalid %s: %w", file.Name, err)
			}
			dp.Meta = meta
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
			// Trim CR (Windows-authored files) and skip blank/comment lines,
			// which would otherwise end up as malformed exec env entries.
			for _, line := range strings.Split(string(envFile), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				dp.Env = append(dp.Env, line)
			}
		}
	}
	// Multi-file packages are not supported yet (translation assumes one
	// root function file); previously the last entry silently won, which
	// mistranslated multi-module uploads from a fragment. Reject explicitly
	// until multi-file support lands.
	if len(rootNames) > 1 {
		return nil, fmt.Errorf("multiple source files found (%s): a package must contain exactly one .py/.go root file", strings.Join(rootNames, ", "))
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
