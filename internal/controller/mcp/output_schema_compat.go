package mcp

import "encoding/json"

func compatibleOutputSchema(schemaJSON json.RawMessage) json.RawMessage {
	var schema any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return schemaJSON
	}

	compatible := makeSchemaCompatible(schema)

	b, err := json.Marshal(compatible)
	if err != nil {
		return schemaJSON
	}

	return json.RawMessage(b)
}

func makeSchemaCompatible(schema any) any {
	switch s := schema.(type) {
	case map[string]any:
		return makeObjectSchemaCompatible(s)
	case []any:
		for i, item := range s {
			s[i] = makeSchemaCompatible(item)
		}

		return s
	default:
		return schema
	}
}

func makeObjectSchemaCompatible(schema map[string]any) any {
	convertNullableSchema(schema)

	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions"} {
		if values, ok := schema[key].(map[string]any); ok {
			for name, value := range values {
				values[name] = makeSchemaCompatible(value)
			}
		}
	}

	for _, key := range []string{"items", "additionalItems", "additionalProperties", "contains", "propertyNames", "unevaluatedItems", "unevaluatedProperties", "not"} {
		if value, ok := schema[key]; ok {
			schema[key] = makeSchemaCompatible(value)
		}
	}

	for _, key := range []string{"oneOf", "anyOf", "allOf", "prefixItems"} {
		if values, ok := schema[key].([]any); ok {
			for i, value := range values {
				values[i] = makeSchemaCompatible(value)
			}
		}
	}

	if values, ok := schema["oneOf"]; ok {
		delete(schema, "oneOf")
		schema["anyOf"] = values
	}

	if schemaAllowsType(schema, "object") || schema["properties"] != nil {
		delete(schema, "required")
		if _, ok := schema["additionalProperties"]; !ok {
			schema["additionalProperties"] = true
		}

		return schema
	}

	if hasSchemaAlternatives(schema) {
		return schema
	}

	if shouldAllowRuntimeShapeDrift(schema) {
		return withRuntimeShapeDrift(schema)
	}

	return schema
}

func hasSchemaAlternatives(schema map[string]any) bool {
	for _, key := range []string{"oneOf", "anyOf"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}

	return false
}

func convertNullableSchema(schema map[string]any) {
	nullable, ok := schema["nullable"].(bool)
	if !ok || !nullable {
		return
	}

	delete(schema, "nullable")

	if values, ok := schema["oneOf"].([]any); ok {
		schema["oneOf"] = appendNullType(values)
		return
	}

	if values, ok := schema["anyOf"].([]any); ok {
		schema["anyOf"] = appendNullType(values)
		return
	}

	switch typ := schema["type"].(type) {
	case string:
		if typ != "null" {
			schema["type"] = []any{typ, "null"}
		}
	case []any:
		schema["type"] = appendNullString(typ)
	default:
		schema["type"] = []any{"object", "null"}
	}
}

func appendNullType(values []any) []any {
	for _, value := range values {
		if m, ok := value.(map[string]any); ok && schemaAllowsType(m, "null") {
			return values
		}
	}

	return append(values, map[string]any{"type": "null"})
}

func appendNullString(values []any) []any {
	for _, value := range values {
		if typ, ok := value.(string); ok && typ == "null" {
			return values
		}
	}

	return append(values, "null")
}

func schemaAllowsType(schema map[string]any, want string) bool {
	for _, typ := range schemaTypes(schema["type"]) {
		if typ == want {
			return true
		}
	}

	return false
}

func shouldAllowRuntimeShapeDrift(schema map[string]any) bool {
	for _, typ := range schemaTypes(schema["type"]) {
		switch typ {
		case "string", "integer", "number", "boolean", "array", "object":
			return true
		}
	}

	return false
}

func withRuntimeShapeDrift(schema map[string]any) any {
	return map[string]any{
		"anyOf": []any{
			schema,
			map[string]any{"type": "null"},
			map[string]any{
				"type":                 "object",
				"additionalProperties": true,
			},
			map[string]any{
				"type":  "array",
				"items": map[string]any{},
			},
			map[string]any{"type": "string"},
			map[string]any{"type": "number"},
			map[string]any{"type": "boolean"},
		},
	}
}
