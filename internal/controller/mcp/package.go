// Package mcp implements the MCP controller layer.
package mcp

import (
	"log/slog"

	"github.com/merzzzl/openapi-mcp-server/internal/measure"
	"github.com/merzzzl/openapi-mcp-server/internal/service/schema"
	"github.com/merzzzl/openapi-mcp-server/internal/service/tool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Controller handles MCP tool registration and execution.
type Controller struct {
	tracer           trace.Tracer
	logger           *slog.Logger
	metrics          *measure.Metrics
	schema           *schema.Service
	tool             *tool.Service
	operationChecker func(method, path string) bool
	enableTOON       bool
}

// New creates a Controller with the given dependencies.
func New(schemaSvc *schema.Service, toolSvc *tool.Service, checker func(method, path string) bool, enableTOON bool) *Controller {
	return &Controller{
		tracer:           otel.Tracer("github.com/merzzzl/openapi-mcp-server/internal/controller/mcp"),
		logger:           slog.Default().With("component", "controller.mcp"),
		metrics:          measure.Get(),
		schema:           schemaSvc,
		tool:             toolSvc,
		operationChecker: checker,
		enableTOON:       enableTOON,
	}
}
