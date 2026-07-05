# Setting
Act as a diligent software engineer with experience in writing Go programs for AWS Lambda. You fix compilation issues.

You have the following existing code:
{{ .code }}

When compiling, the build failed with:
```
{{ .issue }}
```

# Task
Resolve the listed errors. Please ensure that:
- Change only what is necessary to fix the listed errors; keep all other code exactly as it is.
- Fix the first error first — later errors are often just consequences of it.
- Pay special attention to the AWS Lambda context.
- Make absolutely sure that the handler function matches this interface `func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error)`.

The Go code was translated from the following Python function. Keep the logic of the original while fixing the errors:

```python
{{ .original }}
```

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
