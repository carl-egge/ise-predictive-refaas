# Task
Translate the following Python AWS Lambda function to Go. Preserve functionality, optimize for minimal overhead, and adhere to AWS Lambda conventions.

## Requirements
1. **Handler Signature**: Use `func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error)`.
2. **Code Structure**:
   - Use `package main`.
   - Do not include a `main` function.
   - Include all required imports (e.g., `"context"`, `"encoding/json"`, `"github.com/aws/aws-lambda-go/events"`).
3. **Error Handling**: Explicitly handle errors (e.g., JSON parsing) and return appropriate HTTP status codes.
4. **Output Format**: Return a JSON object with exactly one key, `main.go`, containing the complete Go source. Do not include `go.mod`, `go.sum`, or any other files — dependency resolution is handled automatically from your imports.

## Examples
### JSON Marshaling
Python:
```python
jsonStr = json.dumps({"message": "hello world"})
```
Go:
```go
jsonStr, err := json.Marshal(map[string]interface{}{"message": "hello world"})
if err != nil {
    jsonStr = []byte(`{"error":"could not generate json"}`)
}
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
        return events.APIGatewayProxyResponse{StatusCode: http.StatusBadRequest, Body: "Invalid input"}, nil
    }
    return events.APIGatewayProxyResponse{
        StatusCode: http.StatusOK,
        Body:       fmt.Sprintf("%v", map[string]interface{}{"result": request.Num1 + request.Num2}),
    }, nil
}
```

## Input
Python code:
{{ .code }}

Expected output:
```json
{{ .output }}
```

## Output
Return only the code in this JSON format:
```json
{
  "main.go": "package main\n\nimport (...)\n\nfunc handle(...) { ... }"
}
```