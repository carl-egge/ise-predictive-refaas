# Task
Translate the following Python AWS Lambda function to Go. Preserve the exact behavior: for every input, the Go handler must produce the same response the Python function would.

# Execution Contract
- The handler receives the invocation event as raw JSON: `func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error)`. Parse the fields you need from `event` yourself.
- The response is compared field by field against the Python function's return value: `StatusCode` must match the Python `statusCode`, and `Body` must be a **JSON-encoded string** — build it with `json.Marshal` and convert with `string(...)`. Never use `fmt.Sprintf("%v", ...)` for the body: it prints Go map syntax (`map[result:3]`), not JSON.
- Mirror the Python function's error branches: where Python returns an error dict with a non-200 statusCode (or raises an exception), return the same statusCode and body. Return a Go `error` only for genuinely unhandled failures.
- **Diagnostics go to stderr, never stdout.** The test harness reports the response on stdout. Mirror Python's `logging` (which writes to stderr) with `log.New(os.Stderr, ...)` or `fmt.Fprintln(os.Stderr, ...)` — never `fmt.Println`/`fmt.Printf` and never `log.New(os.Stdout, ...)`.
- **AWS clients must honour the endpoint override.** The function is tested against a local AWS emulator, so every AWS SDK client must use the endpoint in `AWS_ENDPOINT_URL` when that variable is set (and path-style addressing for S3), exactly as the Python original does with `boto3.client(..., endpoint_url=os.getenv("AWS_ENDPOINT_URL"))`. A client that hardcodes its configuration talks to the wrong place and fails every test.

## Requirements
1. **Handler Signature**: Use `func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error)`.
2. **Code Structure**:
   - Use `package main`.
   - Do not include a `main` function.
   - Include all required imports (e.g., `"context"`, `"encoding/json"`, `"github.com/aws/aws-lambda-go/events"`).
3. **Output Format**: Return a JSON object with exactly one key, `main.go`, containing the complete Go source. Do not include `go.mod`, `go.sum`, or any other files — dependency resolution is handled automatically from your imports.

## Python → Go pitfalls to handle deliberately
- `event.get("key", default)`: fields absent from the event must fall back to the same default value, not to Go zero values.
- Python `True`/`False`/`None` become JSON `true`/`false`/`null` — never the strings `"True"` or `"None"`.
- f-strings and `str(...)` map to `fmt.Sprintf`; check that number formatting matches the Python output.
- Python `/` is float division (`3/2 == 1.5`) and `//` is floor division; choose Go types and operators accordingly.
- Build JSON bodies with `json.Marshal`, not manual string concatenation.

## Examples
### Response Body Construction
Python:
```python
return {"statusCode": 200, "body": json.dumps({"result": result})}
```
Go:
```go
body, err := json.Marshal(map[string]interface{}{"result": result})
if err != nil {
    return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError, Body: `{"error":"could not generate json"}`}, nil
}
return events.APIGatewayProxyResponse{StatusCode: http.StatusOK, Body: string(body)}, nil
```

### Input Handling
```go
type requestBody struct {
    Num1 float64 `json:"num1"`
    Num2 float64 `json:"num2"`
}

func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error) {
    var request requestBody
    if err := json.Unmarshal(event, &request); err != nil {
        return events.APIGatewayProxyResponse{StatusCode: http.StatusBadRequest, Body: `{"error":"invalid input"}`}, nil
    }
    body, _ := json.Marshal(map[string]interface{}{"result": request.Num1 + request.Num2})
    return events.APIGatewayProxyResponse{StatusCode: http.StatusOK, Body: string(body)}, nil
}
```

## Input
Python code:
{{ .code }}

{{ if .lib_hints }}## Library mapping
Use these Go equivalents for the Python libraries this function imports:

{{ .lib_hints }}

{{ end }}{{ if .py_features }}## Constructs needing attention
This source uses Python constructs with no direct Go form:

{{ .py_features }}

{{ end }}
{{ if .tests }}The translation must satisfy these test cases (input event → expected response of the Python function):

{{ .tests }}{{ else }}Expected output for one input:
```json
{{ .output }}
```{{ end }}

## Output
Return only the code in this JSON format:
```json
{
  "main.go": "package main\n\nimport (...)\n\nfunc handle(...) { ... }"
}
```
