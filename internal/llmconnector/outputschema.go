package llmconnector

// OutputField describes one expected key in an LLM task's JSON response.
// Unless overridden, a field defaults to a nullable string - the shape every
// existing prompt/reader in this project already produces and expects.
type OutputField struct {
	Type        string
	Nullable    bool
	Description string
}

// OutputSchema maps expected response keys to their field definitions. It is
// built per task from task_args.output_keys (see ParseOutputSchema) and
// consumed by every connector's Prepare to constrain what the LLM is allowed
// to return on the following InvokeLLM call.
type OutputSchema map[string]OutputField

// ParseOutputSchema interprets a task's output_keys task_arg - a
// map[string]interface{} decoded from the pipeline's YAML/JSON - into an
// OutputSchema, defaulting each field's Type to "string" and Nullable to
// true unless explicitly overridden. Returns nil if raw doesn't describe any
// fields, so callers can fall back to their own default schema.
func ParseOutputSchema(raw interface{}) OutputSchema {
	fields, ok := raw.(map[string]interface{})
	if !ok || len(fields) == 0 {
		return nil
	}

	schema := make(OutputSchema, len(fields))
	for key, v := range fields {
		field := OutputField{Type: "string", Nullable: true}
		if def, ok := v.(map[string]interface{}); ok {
			if t, ok := def["type"].(string); ok && t != "" {
				field.Type = t
			}
			if n, ok := def["nullable"].(bool); ok {
				field.Nullable = n
			}
			if d, ok := def["description"].(string); ok {
				field.Description = d
			}
		}
		schema[key] = field
	}
	return schema
}

// JSONSchemaProperties renders the schema as a JSON Schema "properties"
// object - the shape Ollama's structured-output Format and ChatAI's
// json_schema response_format both expect. Nullable fields use a type array
// (e.g. ["string", "null"]) per the JSON Schema spec, rather than a separate
// "nullable" keyword.
func (s OutputSchema) JSONSchemaProperties() map[string]interface{} {
	props := make(map[string]interface{}, len(s))
	for key, field := range s {
		var typ interface{} = field.Type
		if field.Nullable {
			typ = []string{field.Type, "null"}
		}
		prop := map[string]interface{}{"type": typ}
		if field.Description != "" {
			prop["description"] = field.Description
		}
		props[key] = prop
	}
	return props
}
