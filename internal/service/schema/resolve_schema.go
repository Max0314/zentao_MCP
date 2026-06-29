package schema

import (
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

func (s *Service) resolveSchema(doc *openapi3.T, ref *openapi3.SchemaRef, visited map[*openapi3.Schema]bool) *openapi3.Schema {
	if ref == nil || ref.Value == nil {
		return nil
	}

	if ref.Ref != "" {
		refName := strings.TrimPrefix(ref.Ref, "#/components/schemas/")

		if resolved, ok := doc.Components.Schemas[refName]; ok {
			ref = resolved
		}
	}

	if ref.Value == nil {
		return nil
	}

	if visited[ref.Value] {
		return openapi3.NewSchema()
	}

	visited[ref.Value] = true

	schema := copySchemaScalars(ref.Value)

	for _, nested := range ref.Value.OneOf {
		if cp := s.resolveSchema(doc, nested, visited); cp != nil {
			schema.OneOf = append(schema.OneOf, cp.NewRef())
		}
	}

	for _, nested := range ref.Value.AnyOf {
		if cp := s.resolveSchema(doc, nested, visited); cp != nil {
			schema.AnyOf = append(schema.AnyOf, cp.NewRef())
		}
	}

	for _, nested := range ref.Value.AllOf {
		if cp := s.resolveSchema(doc, nested, visited); cp != nil {
			schema.AllOf = append(schema.AllOf, cp.NewRef())
		}
	}

	if ref.Value.Not != nil {
		if cp := s.resolveSchema(doc, ref.Value.Not, visited); cp != nil {
			schema.Not = cp.NewRef()
		}
	}

	if ref.Value.Items != nil {
		if cp := s.resolveSchema(doc, ref.Value.Items, visited); cp != nil {
			schema.WithItems(cp)
		}
	}

	for name, property := range ref.Value.Properties {
		if cp := s.resolveSchema(doc, property, visited); cp != nil {
			schema.WithProperty(name, cp)
		}
	}

	if ref.Value.AdditionalProperties.Schema != nil {
		ap := ref.Value.AdditionalProperties

		if cp := s.resolveSchema(doc, ap.Schema, visited); cp != nil {
			schema.AdditionalProperties = openapi3.AdditionalProperties{
				Has:    ap.Has,
				Schema: cp.NewRef(),
			}
		}
	}

	delete(visited, ref.Value)

	return schema
}

func copySchemaScalars(src *openapi3.Schema) *openapi3.Schema {
	dst := openapi3.NewSchema()
	dst.Type = src.Type
	dst.Format = src.Format
	dst.Title = src.Title
	dst.Description = src.Description
	dst.Enum = src.Enum
	dst.Default = src.Default
	dst.Example = src.Example
	dst.Required = src.Required
	dst.ReadOnly = src.ReadOnly
	dst.WriteOnly = src.WriteOnly
	dst.AllowEmptyValue = src.AllowEmptyValue
	dst.Deprecated = src.Deprecated
	dst.Min = src.Min
	dst.Max = src.Max
	dst.MultipleOf = src.MultipleOf
	dst.MinLength = src.MinLength
	dst.MaxLength = src.MaxLength
	dst.Pattern = src.Pattern
	dst.MinItems = src.MinItems
	dst.MaxItems = src.MaxItems
	dst.UniqueItems = src.UniqueItems
	dst.Nullable = src.Nullable
	dst.MinProps = src.MinProps
	dst.MaxProps = src.MaxProps
	dst.ExclusiveMin = src.ExclusiveMin
	dst.ExclusiveMax = src.ExclusiveMax

	return dst
}
