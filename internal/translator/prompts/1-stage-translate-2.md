# Task
Translate the following Python AWS Lambda function to Go. Preserve the exact behavior: for every input, the Go handler must produce the same response the Python function would.

# Execution Contract
- The handler receives the invocation event as raw JSON and returns whatever the Python function returns: `func handle(ctx context.Context, event json.RawMessage) (any, error)`. Parse the fields you need from `event` yourself.
- **The return value must mirror the Python function's return value exactly.** This is the single most important requirement, and the return type is `any` precisely so you can match any shape:
  - Python returns a dict like `{"statusCode": 200, "body": "..."}` → return that same shape. `events.APIGatewayProxyResponse{StatusCode: ..., Body: ...}` is fine here, as is a plain `map[string]any`.
  - Python returns any **other** dict (an Alexa/Lex response such as `{"version": "1.0", "sessionAttributes": {}, "response": {...}}`, or any custom object) → return that dict as-is. Do **not** wrap it in an API Gateway response.
  - Python returns `None` (common for functions whose real work is a side effect) → `return nil, nil`. Do **not** invent a `{"statusCode": 200}` response.
  - Python returns a list, string or number → return that value directly.
- When the Python return value has a `body` that is a JSON **string** (built with `json.dumps`), the Go `body` must also be a JSON-encoded **string** — build it with `json.Marshal` and convert with `string(...)`. Never use `fmt.Sprintf("%v", ...)` for it: that prints Go map syntax (`map[result:3]`), not JSON.
- Mirror the Python function's error branches: where Python returns an error value (or raises an exception), return the same value. Return a Go `error` only for genuinely unhandled failures.
- **Diagnostics go to stderr, never stdout.** The test harness reports the response on stdout. Mirror Python's `logging` (which writes to stderr) with `log.New(os.Stderr, ...)` or `fmt.Fprintln(os.Stderr, ...)` — never `fmt.Println`/`fmt.Printf` and never `log.New(os.Stdout, ...)`.
- **AWS clients must honour the endpoint override.** The function is tested against a local AWS emulator, so every AWS SDK client must use the endpoint in `AWS_ENDPOINT_URL` when that variable is set (and path-style addressing for S3), exactly as the Python original does with `boto3.client(..., endpoint_url=os.getenv("AWS_ENDPOINT_URL"))`. A client that hardcodes its configuration talks to the wrong place and fails every test.

## Requirements
1. **Handler Signature**: Use `func handle(ctx context.Context, event json.RawMessage) (any, error)`, and return the same shape the Python function returns (see the Execution Contract above).
2. **Code Structure**:
   - Use `package main`.
   - Do not include a `main` function.
   - Include all required imports (e.g., `"context"`, `"encoding/json"`). Import `"github.com/aws/aws-lambda-go/events"` only if you actually use it.
3. **Output Format**: Return a JSON object with exactly one key, `main.go`, containing the complete Go source. Do not include `go.mod`, `go.sum`, or any other files — dependency resolution is handled automatically from your imports.

## Python → Go pitfalls to handle deliberately
- `event.get("key", default)`: fields absent from the event must fall back to the same default value, not to Go zero values.
- Python `True`/`False`/`None` become JSON `true`/`false`/`null` — never the strings `"True"` or `"None"`.
- f-strings and `str(...)` map to `fmt.Sprintf`; check that number formatting matches the Python output.
- Python `/` is float division (`3/2 == 1.5`) and `//` is floor division; choose Go types and operators accordingly.
- Build JSON bodies with `json.Marshal`, not manual string concatenation.
- **Never use a Go keyword as an identifier.** `func`, `type`, `range`, `select`, `map`, `chan`, `interface`, `package`, `import`, `var`, `const`, `go`, `defer`, `return` are reserved and cannot name a parameter, variable or field. A Python helper like `def try_ex(func):` must become `func tryEx(fn func() any) any`, never `func tryEx(func func() any)` — the latter does not parse, and the compiler reports it only as `syntax error: unexpected keyword func`, which does not name the real problem.
- **Go rejects unused variables and imports.** Every declared variable must be read; delete anything you do not use, or assign it to `_`. A `declared and not used` error fails the whole build, so do not leave a translated-but-unused Python local in place.
- **AWS SDK v2 takes values, not pointers, in many places where v1 took pointers.** `aws.Bool(x)` / `aws.Int32(n)` return `*bool` / `*int32`; only use them where the struct field is a pointer, and pass the plain value where it is not.

## Examples
Match the Python return shape. All three of these are correct translations — which one is right depends entirely on what the Python function returns.

### A. Python returns an API Gateway response
Python:
```python
return {"statusCode": 200, "body": json.dumps({"result": result})}
```
Go:
```go
body, err := json.Marshal(map[string]any{"result": result})
if err != nil {
    return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError, Body: `{"error":"could not generate json"}`}, nil
}
return events.APIGatewayProxyResponse{StatusCode: http.StatusOK, Body: string(body)}, nil
```

### B. Python returns some other dict — return it as-is
Python:
```python
return {"version": "1.0", "sessionAttributes": {}, "response": build_response(text)}
```
Go — note there is **no** API Gateway wrapper; wrapping this would change the response and fail every test:
```go
return map[string]any{
    "version":           "1.0",
    "sessionAttributes": map[string]any{},
    "response":          buildResponse(text),
}, nil
```

### C. Python returns None — the work is the side effect
Python:
```python
cloudwatch.put_metric_data(Namespace=ns, MetricData=data)
# falls off the end: returns None
```
Go:
```go
if _, err := client.PutMetricData(ctx, input); err != nil {
    return nil, err
}
return nil, nil
```

### Input Handling
```go
type requestBody struct {
    Num1 float64 `json:"num1"`
    Num2 float64 `json:"num2"`
}

func handle(ctx context.Context, event json.RawMessage) (any, error) {
    var request requestBody
    if err := json.Unmarshal(event, &request); err != nil {
        return events.APIGatewayProxyResponse{StatusCode: http.StatusBadRequest, Body: `{"error":"invalid input"}`}, nil
    }
    body, _ := json.Marshal(map[string]any{"result": request.Num1 + request.Num2})
    return events.APIGatewayProxyResponse{StatusCode: http.StatusOK, Body: string(body)}, nil
}
```

## Input
Intent:
{{ .intent }}

Python code:
{{ .code }}

{{ if .lib_hints }}## Library mapping
Use these Go equivalents for the Python libraries this function imports:

{{ .lib_hints }}

{{ end }}{{ if .py_features }}## Constructs needing attention
This source uses Python constructs with no direct Go form:

{{ .py_features }}

{{ end }}{{ if .tests }}The translation must satisfy these test cases (input event → expected response of the Python function):

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
