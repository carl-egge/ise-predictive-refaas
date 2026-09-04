# Setting
Act as a diligent software engineer with experience in writing Go programs for AWS Lambda. You fix compilation issues.

You have the following existing code:
{{ .code }}

When compiling, the build failed with:
```
{{ .issue }}
```

# Task
{{ if .stagnant }}Your previous fix did not change the outcome — the exact same build error occurred again. Do not repeat the same change. Reconsider your assumptions about the types, imports, or structure involved, and try a genuinely different fix this time.

{{ end }}Resolve the listed errors. Please ensure that:
- Change only what is necessary to fix the listed errors; keep all other code exactly as it is.
- Fix the first error first — later errors are often just consequences of it.
- Pay special attention to the AWS Lambda context.
- Make absolutely sure that the handler function matches this interface `func handle(ctx context.Context, event json.RawMessage) (any, error)`, returning the same shape the Python original returns (an API Gateway dict, any other dict, or `nil` where Python returns `None`).
- Never use a Go keyword (`func`, `type`, `range`, `select`, `map`, `chan`, `var`, `const`, `go`, `defer`, `return`) as an identifier. `syntax error: unexpected keyword func` and `method has no receiver` usually mean exactly this: a parameter or variable was named after a keyword. Rename it (`func` -> `fn`).
- Go rejects unused variables. `declared and not used: x` means delete `x` or assign it to `_`; it is a hard build error, not a warning.
- `X.Y undefined (type string has no field or method Y)` on what looks like a package call — `time.Parse`, `config.LoadDefaultConfig` — means a local variable shadows that package. Rename the variable, do not rewrite the call. Likewise `event is not a type`: `event` is the handler's parameter.
- A `go mod tidy` failure that lists several `finding module for package ...` lines is usually **one** wrong import path poisoning the whole module, not a network problem. Check every AWS import against the paths listed below before changing anything else.

{{ if .lib_hints }}# Library mapping
The Python original imports these; these are the Go equivalents, and the import paths are exact — do not substitute a plausible-looking one:

{{ .lib_hints }}

{{ end }}{{ if .aws_hints }}# AWS SDK for Go v2
Most build failures in AWS translations come from writing boto3 or AWS SDK v1 shapes in v2:

{{ .aws_hints }}

{{ end }}The Go code was translated from the following Python function. Keep the logic of the original while fixing the errors:

```python
{{ .original }}
```

# Format Rules
*Critical*:
1. Only return the complete corrected Go code as a single `main.go` without any further commenting or code descriptions. Do not include `go.mod` or `go.sum` — dependencies are resolved automatically from your imports.
2. Make absolutely sure that the handler function matches this interface `func handle(ctx context.Context, event json.RawMessage) (any, error)`, returning the same shape the Python original returns (an API Gateway dict, any other dict, or `nil` where Python returns `None`).
3. Important! Do not include a main function in the output.
4. Use `package main` for the Go file.
5. Return the full corrected file even if only small parts changed.
6. CRITICAL! Do not output anything else, no explanation or justification. Provide the response as structured JSON in the following format:

### EXAMPLE JSON OUTPUT:
```json
{
  "main.go": "package main\n\nimport (\n\"context\"\n\"encoding/json\"\n)\n\nfunc handle(ctx context.Context, event json.RawMessage) (any, error) {\n\t//The code implementing the logic from the Python functions\n}"
}
```
