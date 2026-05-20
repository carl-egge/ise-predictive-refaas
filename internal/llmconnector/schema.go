package llmconnector

import "encoding/json"

var llmOutputSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": {
	"type": "string"
  }
}`)
