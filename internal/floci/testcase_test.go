package floci

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

func TestAssertionUnmarshal(t *testing.T) {
	var a Assertion
	if err := json.Unmarshal([]byte(`{"type":"s3.objectExists","bucket":"b","key":"k"}`), &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if a.Type != "s3.objectExists" {
		t.Fatalf("type = %q", a.Type)
	}
	var spec s3Spec
	if err := json.Unmarshal(a.Spec, &spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	if spec.Bucket != "b" || spec.Key != "k" {
		t.Fatalf("spec = %+v", spec)
	}

	if err := json.Unmarshal([]byte(`{"bucket":"b"}`), &a); err == nil {
		t.Fatal("expected error for assertion without a type")
	}
}

func TestLoadTestCasesFromDir(t *testing.T) {
	dir := t.TempDir()
	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("a.json", `{"name":"a","payload":{"x":1},"sideEffects":[{"type":"s3.objectExists","bucket":"b","key":"k"}]}`)
	must("b.json", `{"payload":{"x":2}}`)
	must("ignore.txt", "not json")

	cases, err := LoadTestCasesFromDir(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(cases))
	}
	if cases[0].Name != "a" {
		t.Errorf("cases[0].Name = %q", cases[0].Name)
	}
	// Name defaults to the file stem when omitted.
	if cases[1].Name != "b" {
		t.Errorf("cases[1].Name = %q, want file-stem default", cases[1].Name)
	}
	if len(cases[0].SideEffects) != 1 || cases[0].SideEffects[0].Type != "s3.objectExists" {
		t.Errorf("side effects not parsed: %+v", cases[0].SideEffects)
	}
}

func TestTestCasesFromPackage(t *testing.T) {
	pkg := &domain.DeploymentPackage{
		TestFiles: map[string]string{
			"t1": `{"name":"t1","input":"{\"id\":\"u1\"}","output":"{\"status\":\"ok\"}"}`,
		},
	}
	cases, err := TestCasesFromPackage(pkg)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("got %d cases", len(cases))
	}
	if string(cases[0].Payload) != `{"id":"u1"}` {
		t.Errorf("payload = %s", cases[0].Payload)
	}
	if string(cases[0].ExpectedOutput) != `{"status":"ok"}` {
		t.Errorf("expected = %s", cases[0].ExpectedOutput)
	}
}

func TestUnknownCheckerAndSetupError(t *testing.T) {
	ctx := context.Background()
	err := runChecker(ctx, &Clients{}, Assertion{Type: "nope.unknown", Spec: []byte(`{}`)})
	if err == nil {
		t.Fatal("expected error for unregistered checker")
	}
	err = runSetup(ctx, &Clients{}, Assertion{Type: "nope.unknown", Spec: []byte(`{}`)})
	if err == nil {
		t.Fatal("expected error for unregistered setup action")
	}
}

func TestBuiltinCheckersRegistered(t *testing.T) {
	for _, typ := range []string{"s3.objectExists", "s3.objectContains", "dynamodb.itemExists"} {
		if _, ok := checkerRegistry[typ]; !ok {
			t.Errorf("checker %q not registered", typ)
		}
	}
	for _, typ := range []string{"s3.bucket", "s3.object", "dynamodb.table", "dynamodb.item"} {
		if _, ok := setupRegistry[typ]; !ok {
			t.Errorf("setup %q not registered", typ)
		}
	}
}
