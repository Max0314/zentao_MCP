package models

import "encoding/json"

// ToolParam describes an API operation parameter.
type ToolParam struct {
	Name string
	In   string
}

// ToolDefinition describes an API operation exposed as an MCP tool.
type ToolDefinition struct {
	OperationID  string
	Method       string
	Path         string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	Params       []ToolParam
	HasBody      bool
	WrapOutput   bool
	// Deprecated mirrors the OpenAPI deprecated flag. The bundled ZenTao
	// document uses it to mark routes that ZenTao 12.3 v1 does not serve.
	Deprecated bool
}
