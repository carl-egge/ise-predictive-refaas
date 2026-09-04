package pipeline

import (
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pyscan"
)

const pySource = `
import json
import boto3

s3 = boto3.client("s3")

def lambda_handler(event, context):
    key = event.get("key")
    if not key:
        return {"statusCode": 400, "body": json.dumps({"error": "no key"})}
    return {"statusCode": 200, "body": "{}"}
`

func pyScanRequest() *domain.ConversionRequest {
	pkg := &domain.DeploymentPackage{
		RootFile: pySource,
		TestFiles: map[string]string{
			"t1.json": `{"name":"t1","payload":{"key":"a"},"expectedOutput":{"statusCode":200}}`,
		},
	}
	return &domain.ConversionRequest{
		SourcePackage:  pkg,
		WorkingPackage: pkg.Copy(),
		Metrics:        &domain.Metrics{},
	}
}

func skipWithoutPython(t *testing.T) {
	t.Helper()
	if !pyscan.Available() {
		t.Skip("no python3 interpreter on PATH")
	}
}

func TestPyScanPublishesPromptMetadata(t *testing.T) {
	skipWithoutPython(t)
	req := pyScanRequest()

	conv, err := MakeConverter("pyScan", nil)
	if err != nil {
		t.Fatalf("pyScan is not registered: %v", err)
	}
	if err := conv.Apply(&Runner{}, req); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	hints := req.Metadata[MetaLibHints]
	if !strings.Contains(hints, "aws-sdk-go-v2") {
		t.Errorf("%s should map boto3 onto the Go SDK, got: %q", MetaLibHints, hints)
	}
	// Naming the service is not enough: the model invented "service/
	// stepfunctions" and "service/iotdata" from names alone in run
	// 20260831-190900, and one bad import path fails `go mod tidy` for the
	// whole module. The hint has to carry the exact path.
	if !strings.Contains(hints, `import "github.com/aws/aws-sdk-go-v2/service/s3"`) {
		t.Errorf("%s should give the exact module path for the constructed service, got: %q", MetaLibHints, hints)
	}
	awsHints := req.Metadata[MetaAWSHints]
	if !strings.Contains(awsHints, "UsePathStyle") {
		t.Errorf("%s should carry the v2 idiom block for an AWS function, got: %q", MetaAWSHints, awsHints)
	}
	// base 1 + the single `if` = 2 (`not` is a unary op, not a BoolOp chain)
	if req.Metadata[MetaCC] != "2" {
		t.Errorf("%s = %q, want \"2\"", MetaCC, req.Metadata[MetaCC])
	}
	// This function uses nothing exotic, so the construct section must stay
	// absent rather than render an empty heading in the prompt.
	if _, present := req.Metadata[MetaPyFeatures]; present {
		t.Errorf("%s should be absent for a plain function, got %q", MetaPyFeatures, req.Metadata[MetaPyFeatures])
	}
	if _, present := req.Metadata[MetaFeasibility]; present {
		t.Error("a boto3 function must not raise a feasibility warning")
	}
}

func TestPyScanRecordsFeatureVectorOnMetrics(t *testing.T) {
	skipWithoutPython(t)
	req := pyScanRequest()

	conv, _ := MakeConverter("pyScan", nil)
	if err := conv.Apply(&Runner{}, req); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	fv := req.Metrics.Features
	if fv == nil {
		t.Fatal("the scan must record its feature vector on the metrics; without it the run log is not training data")
	}
	if fv.SchemaVersion != pyscan.FeatureSchemaVersion {
		t.Errorf("schema version = %d, want %d", fv.SchemaVersion, pyscan.FeatureSchemaVersion)
	}
	if len(fv.Names) != len(fv.Values) || len(fv.Names) == 0 {
		t.Fatalf("misaligned vector: %d names, %d values", len(fv.Names), len(fv.Values))
	}
	// The fixture family must be populated from the package's own fixtures,
	// not silently zeroed.
	var cases float64
	for i, n := range fv.Names {
		if n == "n_test_cases" {
			cases = fv.Values[i]
		}
	}
	if cases != 1 {
		t.Errorf("n_test_cases = %v, want 1", cases)
	}
}

// The stage enriches a prompt; it must not be able to fail a conversion
// unless the pipeline explicitly asked it to.
func TestPyScanDegradesOnUnscannableSource(t *testing.T) {
	req := pyScanRequest()
	req.SourcePackage.RootFile = "def broken(:\n"

	conv, _ := MakeConverter("pyScan", nil)
	if err := conv.Apply(&Runner{}, req); err != nil {
		t.Errorf("an unscannable source must degrade, not fail the conversion: %v", err)
	}
	if len(req.Metadata) != 0 {
		t.Errorf("no hints should be published for an unscannable source, got %v", req.Metadata)
	}
}

func TestPyScanRequiredFailsOnUnscannableSource(t *testing.T) {
	skipWithoutPython(t)
	req := pyScanRequest()
	req.SourcePackage.RootFile = "def broken(:\n"

	conv, _ := MakeConverter("pyScan", map[string]interface{}{"required": true})
	if err := conv.Apply(&Runner{}, req); err == nil {
		t.Fatal("required: true must fail the task, so a benchmark run cannot silently record jobs without features")
	}
}

func TestPyScanMissingSourceDegrades(t *testing.T) {
	req := &domain.ConversionRequest{Metrics: &domain.Metrics{}}
	conv, _ := MakeConverter("pyScan", nil)
	if err := conv.Apply(&Runner{}, req); err != nil {
		t.Errorf("a request without a source package must degrade: %v", err)
	}
}
