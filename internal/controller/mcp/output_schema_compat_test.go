package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestCompatibleOutputSchemaAcceptsIntegerRuntimeShapeDrift(t *testing.T) {
	schemaJSON := json.RawMessage(`{
		"type": "object",
		"properties": {
			"total": {"type": "integer"}
		}
	}`)

	var schema jsonschema.Schema
	if err := json.Unmarshal(compatibleOutputSchema(schemaJSON), &schema); err != nil {
		t.Fatalf("unmarshal compatible schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("resolve compatible schema: %v", err)
	}

	if err := resolved.Validate(map[string]any{"total": 113}); err != nil {
		t.Fatalf("compatible schema should accept integer values: %v", err)
	}
}

func TestCompatibleOutputSchemaConvertsOneOfToAnyOf(t *testing.T) {
	schemaJSON := json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {
				"oneOf": [
					{"type": "string"},
					{"type": "integer"}
				]
			}
		}
	}`)

	var raw map[string]any
	if err := json.Unmarshal(compatibleOutputSchema(schemaJSON), &raw); err != nil {
		t.Fatalf("unmarshal compatible schema: %v", err)
	}
	idSchema := raw["properties"].(map[string]any)["id"].(map[string]any)
	if _, ok := idSchema["oneOf"]; ok {
		t.Fatal("compatible schema should not leave oneOf on drift-prone fields")
	}
	if _, ok := idSchema["anyOf"]; !ok {
		t.Fatal("compatible schema should convert oneOf to anyOf")
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(compatibleOutputSchema(schemaJSON), &schema); err != nil {
		t.Fatalf("unmarshal compatible schema: %v", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("resolve compatible schema: %v", err)
	}
	if err := resolved.Validate(map[string]any{"id": 84736}); err != nil {
		t.Fatalf("compatible schema should accept integer id values: %v", err)
	}
}
