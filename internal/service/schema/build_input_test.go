package schema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestBuildToolInputSchemaAcceptsNumericStringParameters(t *testing.T) {
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name:   "pageID",
					In:     "query",
					Schema: openapi3.NewStringSchema().NewRef(),
				},
			},
		},
	}

	inputSchema := New(nil).buildToolInputSchema(context.Background(), &openapi3.T{}, "", op, nil)
	resolved := resolveInputJSONSchema(t, inputSchema)

	if err := resolved.Validate(map[string]any{"pageID": 1}); err != nil {
		t.Fatalf("expected input schema to accept numeric pageID argument: %v", err)
	}
}

func resolveInputJSONSchema(t *testing.T, schemaJSON json.RawMessage) *jsonschema.Resolved {
	t.Helper()

	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		t.Fatalf("unmarshal input schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("resolve input schema: %v", err)
	}

	return resolved
}
