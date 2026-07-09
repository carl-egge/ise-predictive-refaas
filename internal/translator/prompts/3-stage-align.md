# Setting
Act as a diligent software engineer with experience in translating code between programming languages, in this case from Python to Go. Your job is to make the Go translation behaviorally equivalent to the original Python function: for every given input it must produce the expected output.

# Context
The Go code is executed by a test harness that reads a JSON event, passes it to `handle(ctx, event)` and compares the produced response against the expected output of the original Python function.

You started with this **original** Python version:
```python
{{ .original }}
```

And have already produced the **current** Go version:
{{ .code }}

# Test Results
{{ if .failures }}The current Go version failed the following test cases:

{{ .failures }}

# Task
Fix the Go code so that each listed input produces exactly the expected output. Do not change behavior for inputs that are not listed. Pay attention to the exact response structure: status codes, field names, and the body being a JSON-encoded string.{{ else }}The current Go version failed testing against the original: {{ .issue }}

# Task
Compare both versions and correct any behavioral difference so that the Go code produces the same output as the Python original for the same input. Pay attention to the exact response structure: status codes, field names, and the body being a JSON-encoded string.{{ end }}

# Format Rules
*Critical*:
1. Only return the complete corrected Go code as a single `main.go` without any further commenting or code descriptions. Do not include `go.mod` or `go.sum` — dependencies are resolved automatically from your imports.
2. Make absolutely sure that the handler function matches this interface `func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error)`.
3. Important! Do not include a main function in the output.
4. Use `package main` for the Go file.
5. Return the full corrected file even if only small parts changed.
6. CRITICAL! Do not output anything else, no explanation or justification. Provide the response as structured JSON in the following format:

### EXAMPLE JSON OUTPUT:
```json
{
"main.go": "package main\n\nimport (\n\"github.com/aws/aws-lambda-go/events\"\n\"context\"\n\"encoding/json\"\n\"net/http\"\n)\n\nfunc handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error) {\n\t//The code implementing the logic from the Python functions\n}"
}
```
