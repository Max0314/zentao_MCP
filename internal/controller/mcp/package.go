// Package mcp implements the MCP controller layer.
package mcp

import (
	"log/slog"

	"github.com/merzzzl/openapi-mcp-server/internal/measure"
	"github.com/merzzzl/openapi-mcp-server/internal/service/schema"
	"github.com/merzzzl/openapi-mcp-server/internal/service/tool"
	"github.com/merzzzl/openapi-mcp-server/internal/service/zentao"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Options carries the per-server switches of the MCP controller.
type Options struct {
	// EnableTOON encodes tool results as TOON instead of JSON.
	EnableTOON bool
	// SkipDeprecated hides operations the OpenAPI document marks deprecated,
	// which is how unsupported ZenTao 12.3 routes are flagged.
	SkipDeprecated bool
	// PublishOutputSchema declares each generated tool's output schema.
	// The ZenTao document inlines an ~86 field object per list endpoint, so
	// output schemas are roughly 90% of the tools/list payload. Turning this
	// off keeps the full JSON in the text content but drops the declared
	// schema, the structured content and the normalization pass.
	PublishOutputSchema bool
	// Zentao enables the composite ZenTao tools when non-nil.
	Zentao *zentao.Service
}

// Controller handles MCP tool registration and execution.
type Controller struct {
	tracer              trace.Tracer
	logger              *slog.Logger
	metrics             *measure.Metrics
	schema              *schema.Service
	tool                *tool.Service
	zentao              *zentao.Service
	operationChecker    func(method, path string) bool
	enableTOON          bool
	skipDeprecated      bool
	publishOutputSchema bool
}

// New creates a Controller with the given dependencies.
func New(schemaSvc *schema.Service, toolSvc *tool.Service, checker func(method, path string) bool, opts Options) *Controller {
	return &Controller{
		tracer:              otel.Tracer("github.com/merzzzl/openapi-mcp-server/internal/controller/mcp"),
		logger:              slog.Default().With("component", "controller.mcp"),
		metrics:             measure.Get(),
		schema:              schemaSvc,
		tool:                toolSvc,
		zentao:              opts.Zentao,
		operationChecker:    checker,
		enableTOON:          opts.EnableTOON,
		skipDeprecated:      opts.SkipDeprecated,
		publishOutputSchema: opts.PublishOutputSchema,
	}
}
