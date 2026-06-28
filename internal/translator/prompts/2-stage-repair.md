# Setting
Act as diligent a software engineer with experience in writing Go programs for AWS Lambda, you fix compilation issues.

You have the following existing code:
```
{{ .code }}
```

When compiling you got the following error:
```
{{ .issue }}
```

# Task
Now your task is to do resolve this issue. Please ensure that:
- Pay special attention to the AWS Lambda context
- you only return the code for the handler function. There is absolutely no need to include a main.
- make absolutely sure that the handler function matches this interface `func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error)`.

Remember the original function that we wanted to build came from the following python function. Make sure that we fix the issue in our go function while still keeping the logiic of the original.

{{ .original }}

# Format Rules
*Critical*:
1. Let's work this out in a step by step way to be sure we have the right answer.
2. Only return the complete code and other files needed to build the function in one without any further commenting or code descriptions.
3. Make absolutely sure that the handler function matches this interface `func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error)`.
4. Important! Do not include a main function in the output.
5. CRITICAL! Do not output anything else, no explanation or justification. Please provide a response in a structured JSON to make it easier to use return the code and other required files in the following format:

### EXAMPLE JSON OUTPUT:
```json
{
  "main.go": "package main\n\nimport (\n\"github.com/aws/aws-lambda-go/events\"\n\"context\"\n\"encoding/json\"\n\"net/http\"\n)\n\nfunc handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error) {\n\t//The code implementing the logic from the Python functions\n}",
  "go.mod": "module github.com\/lambda\/function\r\n\r\ngo 1.23.5\r\n\r\nrequire github.com\/aws\/aws-lambda-go v1.24"
}
```