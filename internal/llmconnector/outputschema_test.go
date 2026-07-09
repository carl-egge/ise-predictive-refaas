package llmconnector

import (
	"slices"
	"testing"
)

// TestRequiredKeys verifies that only non-nullable fields become required and
// that the list is sorted for deterministic request payloads.
func TestRequiredKeys(t *testing.T) {
	schema := ParseOutputSchema(map[string]interface{}{
		"main.go": map[string]interface{}{"nullable": false},
		"intent":  map[string]interface{}{"description": "defaults to nullable"},
		"aux.go":  map[string]interface{}{"nullable": false},
	})

	got := schema.RequiredKeys()
	want := []string{"aux.go", "main.go"}
	if !slices.Equal(got, want) {
		t.Errorf("RequiredKeys() = %v, want %v", got, want)
	}
}

// TestJSONSchemaPropertiesNonNullable verifies a non-nullable field renders
// as a plain type instead of a ["string","null"] union.
func TestJSONSchemaPropertiesNonNullable(t *testing.T) {
	schema := ParseOutputSchema(map[string]interface{}{
		"main.go": map[string]interface{}{"nullable": false},
	})

	props := schema.JSONSchemaProperties()
	prop, ok := props["main.go"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing main.go property: %v", props)
	}
	if typ, ok := prop["type"].(string); !ok || typ != "string" {
		t.Errorf(`type = %v, want plain "string" for non-nullable field`, prop["type"])
	}
}
