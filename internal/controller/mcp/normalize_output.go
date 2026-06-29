package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

func normalizeStructuredOutput(schemaJSON json.RawMessage, value any) any {
	var schema map[string]any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return value
	}

	return normalizeValue(schema, value)
}

func normalizeValue(schema map[string]any, value any) any {
	if value == nil {
		return nil
	}

	if alternatives := schemaAlternatives(schema, "oneOf"); len(alternatives) > 0 {
		return normalizeAlternativeValue(alternatives, value)
	}

	if alternatives := schemaAlternatives(schema, "anyOf"); len(alternatives) > 0 {
		return normalizeAlternativeValue(alternatives, value)
	}

	for _, typ := range schemaTypes(schema["type"]) {
		switch typ {
		case "object":
			return normalizeObject(schema, value)
		case "array":
			return normalizeArray(schema, value)
		case "string":
			if _, ok := value.(string); ok {
				return value
			}

			switch value.(type) {
			case map[string]any, []any:
				return value
			}

			return fmt.Sprint(value)
		case "integer":
			if normalized, ok := normalizeInteger(value); ok {
				return normalized
			}

			return value
		case "number":
			if normalized, ok := normalizeNumber(value); ok {
				return normalized
			}

			return value
		case "boolean":
			if normalized, ok := normalizeBoolean(value); ok {
				return normalized
			}

			return value
		}
	}

	if _, ok := schema["properties"]; ok {
		return normalizeObject(schema, value)
	}

	if _, ok := schema["items"]; ok {
		return normalizeArray(schema, value)
	}

	return value
}

func normalizeAlternativeValue(alternatives []map[string]any, value any) any {
	for _, alternative := range alternatives {
		if valueStrictlyMatchesSchema(value, alternative) {
			return value
		}
	}

	for _, alternative := range alternatives {
		if valueMatchesSchema(value, alternative) {
			return normalizeValue(alternative, value)
		}
	}

	return normalizeValue(alternatives[0], value)
}

func normalizeObject(schema map[string]any, value any) any {
	obj, ok := value.(map[string]any)
	if !ok {
		return value
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return value
	}

	normalized := make(map[string]any, len(obj))
	for k, v := range obj {
		normalized[k] = v
	}

	for name, prop := range props {
		propSchema, ok := prop.(map[string]any)
		if !ok {
			continue
		}

		if v, ok := normalized[name]; ok {
			normalized[name] = normalizeValue(propSchema, v)
		}
	}

	return normalized
}

func normalizeArray(schema map[string]any, value any) any {
	items, ok := schema["items"].(map[string]any)
	if !ok {
		return value
	}

	values, ok := value.([]any)
	if !ok {
		return value
	}

	normalized := make([]any, len(values))
	for i, v := range values {
		normalized[i] = normalizeValue(items, v)
	}

	return normalized
}

func schemaAlternatives(schema map[string]any, key string) []map[string]any {
	raw, ok := schema[key].([]any)
	if !ok {
		return nil
	}

	alternatives := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if alternative, ok := item.(map[string]any); ok {
			alternatives = append(alternatives, alternative)
		}
	}

	return alternatives
}

func schemaTypes(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []any:
		types := make([]string, 0, len(v))
		for _, item := range v {
			if typ, ok := item.(string); ok {
				types = append(types, typ)
			}
		}

		return types
	default:
		return nil
	}
}

func valueMatchesSchema(value any, schema map[string]any) bool {
	if alternatives := schemaAlternatives(schema, "oneOf"); len(alternatives) > 0 {
		for _, alternative := range alternatives {
			if valueMatchesSchema(value, alternative) {
				return true
			}
		}

		return false
	}

	if alternatives := schemaAlternatives(schema, "anyOf"); len(alternatives) > 0 {
		for _, alternative := range alternatives {
			if valueMatchesSchema(value, alternative) {
				return true
			}
		}

		return false
	}

	types := schemaTypes(schema["type"])
	if len(types) == 0 {
		return true
	}

	for _, typ := range types {
		if valueMatchesType(value, typ) {
			return true
		}
	}

	return false
}

func valueMatchesType(value any, typ string) bool {
	switch typ {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		_, ok := normalizeInteger(value)
		return ok
	case "number":
		_, ok := normalizeNumber(value)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

func valueStrictlyMatchesSchema(value any, schema map[string]any) bool {
	if alternatives := schemaAlternatives(schema, "oneOf"); len(alternatives) > 0 {
		for _, alternative := range alternatives {
			if valueStrictlyMatchesSchema(value, alternative) {
				return true
			}
		}

		return false
	}

	if alternatives := schemaAlternatives(schema, "anyOf"); len(alternatives) > 0 {
		for _, alternative := range alternatives {
			if valueStrictlyMatchesSchema(value, alternative) {
				return true
			}
		}

		return false
	}

	types := schemaTypes(schema["type"])
	if len(types) == 0 {
		return true
	}

	for _, typ := range types {
		if valueStrictlyMatchesType(value, typ) {
			return true
		}
	}

	return false
}

func valueStrictlyMatchesType(value any, typ string) bool {
	switch typ {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		switch v := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case float64:
			return math.Trunc(v) == v
		default:
			return false
		}
	case "number":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return true
		default:
			return false
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

func normalizeInteger(value any) (any, bool) {
	switch v := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return v, true
	case float64:
		if math.Trunc(v) == v {
			return v, true
		}
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			return parsed, true
		}
	}

	return value, false
}

func normalizeNumber(value any) (any, bool) {
	switch v := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return v, true
	case string:
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			return parsed, true
		}
	}

	return value, false
}

func normalizeBoolean(value any) (any, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		if parsed, err := strconv.ParseBool(v); err == nil {
			return parsed, true
		}
	}

	return value, false
}
