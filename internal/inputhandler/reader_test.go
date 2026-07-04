package inputhandler

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("creating zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("writing zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

func TestReadFromBytesSingleSource(t *testing.T) {
	data := buildZip(t, map[string]string{
		"main.py":      "def handler(event, context):\n    return {}",
		"test/t1.json": `{"input":"{}","output":"{}"}`,
		".env":         "AWS_ENDPOINT_URL=http://localhost:4566\r\n\n# comment\nREGION=eu-1\n",
	})

	dp, err := ReadFromBytes(data)
	if err != nil {
		t.Fatalf("ReadFromBytes: %v", err)
	}
	if dp.Suffix != "py" || !strings.Contains(dp.RootFile, "def handler") {
		t.Errorf("unexpected root file: suffix=%q root=%q", dp.Suffix, dp.RootFile)
	}
	if len(dp.TestFiles) != 1 {
		t.Errorf("TestFiles = %d, want 1", len(dp.TestFiles))
	}
	// CR trimmed, comment and blank lines skipped
	want := []string{"AWS_ENDPOINT_URL=http://localhost:4566", "REGION=eu-1"}
	if len(dp.Env) != len(want) || dp.Env[0] != want[0] || dp.Env[1] != want[1] {
		t.Errorf("Env = %v, want %v", dp.Env, want)
	}
}

// TestReadFromBytesRejectsMultipleSources guards the maintainer-decided
// policy: multi-file packages are rejected explicitly instead of silently
// translating whichever source file happened to be read last.
func TestReadFromBytesRejectsMultipleSources(t *testing.T) {
	data := buildZip(t, map[string]string{
		"main.py":   "def handler(event, context):\n    return {}",
		"helper.py": "def helper():\n    return 1",
	})

	_, err := ReadFromBytes(data)
	if err == nil {
		t.Fatal("expected an error for a zip with two source files")
	}
	if !strings.Contains(err.Error(), "main.py") || !strings.Contains(err.Error(), "helper.py") {
		t.Errorf("error should name the conflicting files, got: %v", err)
	}
}

// TestReadFromBytesSkipsMacJunk verifies that macOS AppleDouble entries
// neither clobber the root file nor count as a second source file.
func TestReadFromBytesSkipsMacJunk(t *testing.T) {
	data := buildZip(t, map[string]string{
		"main.py":            "def handler(event, context):\n    return {}",
		"__MACOSX/._main.py": "\x00\x05\x16\x07appledouble junk",
		"._main.py":          "\x00\x05\x16\x07appledouble junk",
	})

	dp, err := ReadFromBytes(data)
	if err != nil {
		t.Fatalf("ReadFromBytes: %v", err)
	}
	if !strings.Contains(dp.RootFile, "def handler") {
		t.Errorf("root file was clobbered by a junk entry: %q", dp.RootFile)
	}
}
