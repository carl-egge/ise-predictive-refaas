# Setting
Act as diligent software engineer with experience in translating code between programming languages, in this case from Python to Go, you have been tasked to translate some AWS-Lambda python function to Go.

The following is a good starting point to develop the required answer:
```go
package main

import (
 "github.com/aws/aws-lambda-go/events"
 "context"
 "encoding/json"
 "net/http"
)

func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error) {
	//The code implementing the logic from the Python functions
	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusBadRequest,
		Body:       "Not yet implemented",
	}, nil
}
```

Remember the equivalent to `jsonStr = json.dumps({"message":"hello world"})` in python looks like this:
```go
    jsonStr, err := json.Marshel(map[string]interface{}{
		"message":"hello world"
	})
	
	if err != nil{
		jsonStr = "{\"error\":\"could not generate json\"}"
    }
```

Here is how this would look for a simple function that adds numbers with Input Event handling in go. 
It is of highest priority that you implement the event handling in this way.

Go input handling example:

```go
package main

import (
	"fmt"
	"github.com/aws/aws-lambda-go/events"
	"context"
	"encoding/json"
	"log"
	"net/http"
)

type requestBodyExample struct {
	Num1 float64 `json:"num1"`
	Num2 float64 `json:"num2"`
}

func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error) {
	var request requestBodyExample
	if err := json.Unmarshal(event, &request); err != nil {
		log.Printf("Failed to unmarshal event: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusBadRequest,
			Body:       "Invalid input",
		}, nil
	}
	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       fmt.Sprintf("%v", map[string]interface{}{"result": request.Num1 + request.Num2}),
	}, nil
}
```

# Task
Now follows the python code that should be translated to go:

Please ensure that:

- the functionality remains the same
- Optimize for minimal code and overhead
- the import format is consistent with Go standards-
- Pay special attention to the AWS Lambda context
- you only return the code for the handler function no need to include a main
- make absolutely sure that the handler function matches this interface `func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error)`.


{{ .code }}

Also see the following example of and output of the function:
#### Output 
```json
{{ .output }}
```

# Additional Rules:
*Critical*:
1. Let's work this out in a step by step way to be sure we have the right answer.
2. Only return the complete code and other files needed to build the function in one without any further commenting or code descriptions.
3. Make absolutely sure that the handler function matches this interface `func handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error)`.
4. Important! Do not include a main function in the output.
5. Use the `package main` for any go file.
6. Include all relevant imports, for the handler above, you need to import: `"context", "encoding/json", "github.com/aws/aws-lambda-go/events"`
7. CRITICAL! Do not output anything else, no explanation or justification. Please provide a response in a structured JSON to make it easier to use return the code and other required files in the following format:
### EXAMPLE Output:
```json
{
 "main.go": "package main\n\nimport (\n\"github.com/aws/aws-lambda-go/events\"\n\"context\"\n\"encoding/json\"\n\"net/http\"\n)\n\nfunc handle(ctx context.Context, event json.RawMessage) (events.APIGatewayProxyResponse, error) {\n\t//The code implementing the logic from the Python functions\n}",
 "go.mod": "module github.com\/lambda\/function\r\n\r\ngo 1.23.5\r\n\r\nrequire github.com\/aws\/aws-lambda-go v1.24"
}
```