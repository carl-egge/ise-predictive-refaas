package translator

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestRemoveGoMainMethodKeepsPackageClause guards against a regression where
// rebuilding the file from node.Decls (which excludes the package clause)
// silently dropped "package main" whenever the LLM's response included its
// own func main(), producing an unbuildable main.go
// ("expected 'package', found 'import'").
func TestRemoveGoMainMethodKeepsPackageClause(t *testing.T) {
	reader := GoJsonOllamaReader{}
	input := `package main

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"net/http"
)

func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error) {
	return events.APIGatewayProxyResponse{StatusCode: http.StatusOK}, nil
}

func main() {
	lambda.Start(handle)
}
`
	out := reader.removeGoMainMethod(input)

	if !strings.HasPrefix(strings.TrimSpace(out), "package main") {
		t.Fatalf("expected output to start with 'package main', got:\n%s", out)
	}
	if strings.Contains(out, "func main(") {
		t.Fatalf("expected func main to be removed, got:\n%s", out)
	}

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "main.go", out, parser.AllErrors); err != nil {
		t.Fatalf("rebuilt file is not valid Go source: %v\n%s", err, out)
	}
}
