package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/evaluation/harness"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// findPython locates the interpreter for the Python side. PYSCAN_PYTHON is
// reused rather than introducing a second variable: it already names "the
// interpreter this repo runs Python with", and having the analysis stage and
// the measurement harness disagree about which Python is in use would be a
// nasty source of unexplained differences.
func findPython() (string, error) {
	candidates := []string{os.Getenv("PYSCAN_PYTHON"), "python3", "python"}
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no python3 interpreter found (set PYSCAN_PYTHON)")
}

// PythonEnv records the interpreter the Python side actually ran under, and
// what was installed in it.
//
// It belongs in the report because these packages run *inside* the measured
// region: handler.py imports the dataset function before invoking it, so
// boto3's import cost is a large part of the Python side's startup term. A
// different resolution on a later machine moves the Python-vs-Go ratio with
// nothing in the pipeline having changed - which is exactly the kind of
// difference that is impossible to explain months later if the versions were
// not written down beside the numbers they produced.
type PythonEnv struct {
	Interpreter string            `json:"interpreter"`
	Version     string            `json:"version"`
	Packages    map[string]string `json:"packages,omitempty"`
	Note        string            `json:"note,omitempty"`
}

// pythonProbe asks the interpreter to describe itself. importlib.metadata is
// used rather than `pip freeze` because pip need not be installed in the
// interpreter under test, while importlib.metadata is stdlib from 3.8.
const pythonProbe = `
import json, sys
try:
    from importlib.metadata import distributions
    pkgs = {}
    for d in distributions():
        name = d.metadata["Name"]
        if name:
            pkgs[name] = d.version
except Exception:
    pkgs = {}
json.dump({"version": sys.version.split()[0], "packages": pkgs}, sys.stdout)
`

// describePython probes the interpreter for its version and installed
// distributions. A probe failure is recorded as a note rather than failing
// the run: it is provenance, and losing it must not cost a measurement pass.
func describePython(python string) PythonEnv {
	env := PythonEnv{Interpreter: python}
	out, err := exec.Command(python, "-c", pythonProbe).Output()
	if err != nil {
		env.Note = fmt.Sprintf("could not probe interpreter: %v", err)
		return env
	}
	var probed struct {
		Version  string            `json:"version"`
		Packages map[string]string `json:"packages"`
	}
	if err := json.Unmarshal(out, &probed); err != nil {
		env.Note = fmt.Sprintf("unparseable probe output: %v", err)
		return env
	}
	env.Version, env.Packages = probed.Version, probed.Packages
	return env
}

// writeHarness materializes the Python harness into the scratch directory.
func writeHarness(work string) (string, error) {
	path := filepath.Join(work, "handler.py")
	if err := os.WriteFile(path, harness.Python, 0o644); err != nil {
		return "", fmt.Errorf("writing python harness: %w", err)
	}
	return path, nil
}

// writePythonSource lays the original function out next to nothing else, so
// an accidental import of a sibling file cannot change what is measured.
func writePythonSource(dir, source string) (string, error) {
	pyDir := filepath.Join(dir, "python")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(pyDir, "main.py")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// buildGoBinary compiles the translated package together with the bench
// harness and returns the executable's path.
//
// go.mod is regenerated rather than trusted, matching [C3]: the archived
// package may carry a module file written by an LLM, and a build failure here
// would be reported as "this function is unmeasurable" when it is really a
// packaging artefact.
func buildGoBinary(dir, goSource string, pkg *domain.DeploymentPackage) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(goSource), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "bench_handler.go"), harness.GoBench, 0o644); err != nil {
		return "", err
	}

	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, truncate(string(out), 600))
		}
		return nil
	}

	if err := run("go", "mod", "init", "refaasbench"); err != nil {
		return "", err
	}
	if err := run("go", "mod", "tidy"); err != nil {
		return "", err
	}
	// Optimised, stripped build: the deployed artifact is what the comparison
	// is about, not a debug build.
	if err := run("go", "build", "-ldflags", "-s -w", "-o", "fn", "."); err != nil {
		return "", err
	}
	return filepath.Join(dir, "fn"), nil
}

// loadTranslations reads the translated Go sources, keyed by function id.
//
// Accepts either a directory of per-function .zip packages (what the batch
// script archives) or a single packages-*.zip containing them, because both
// forms exist in runs/ already.
func loadTranslations(path string) (map[string]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading -packages %s: %w", path, err)
	}

	out := map[string]string{}
	if info.IsDir() {
		entries, _ := filepath.Glob(filepath.Join(path, "*.zip"))
		for _, entry := range entries {
			id, source, err := readGoFromZip(entry)
			if err != nil || source == "" {
				continue
			}
			out[id] = source
		}
		// A directory of loose per-function folders is also supported.
		if len(out) == 0 {
			dirs, _ := os.ReadDir(path)
			for _, d := range dirs {
				if !d.IsDir() {
					continue
				}
				data, err := os.ReadFile(filepath.Join(path, d.Name(), "main.go"))
				if err != nil {
					continue
				}
				out[strings.TrimSuffix(d.Name(), ".zip")] = string(data)
			}
		}
	} else {
		nested, err := readNestedPackages(path)
		if err != nil {
			return nil, err
		}
		out = nested
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no translated Go packages found under %s", path)
	}
	return out, nil
}

// readGoFromZip pulls main.go out of one translated package archive.
func readGoFromZip(path string) (string, string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", "", err
	}
	defer r.Close()

	id := strings.TrimSuffix(filepath.Base(path), ".zip")
	for _, f := range r.File {
		if filepath.Base(f.Name) == "main.go" {
			source, err := readZipEntry(f)
			return id, source, err
		}
	}
	return id, "", fmt.Errorf("no main.go in %s", path)
}

// readNestedPackages handles a packages-<run>.zip holding one archive or
// folder per function.
func readNestedPackages(path string) (map[string]string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	out := map[string]string{}
	tmp, err := os.MkdirTemp("", "refaas-packages-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	for _, f := range r.File {
		name := filepath.Base(f.Name)
		switch {
		case name == "main.go":
			// <id>/main.go
			id := filepath.Base(filepath.Dir(f.Name))
			source, err := readZipEntry(f)
			if err == nil && id != "." {
				out[id] = source
			}
		case strings.HasSuffix(name, ".zip"):
			// <id>.zip nested inside; extract then read.
			nestedPath := filepath.Join(tmp, name)
			if err := extractTo(f, nestedPath); err != nil {
				continue
			}
			id, source, err := readGoFromZip(nestedPath)
			if err == nil && source != "" {
				out[id] = source
			}
		}
	}
	return out, nil
}

func readZipEntry(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func extractTo(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}
