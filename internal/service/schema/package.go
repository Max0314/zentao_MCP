// Package schema provides OpenAPI schema analysis and tool generation.
package schema

import (
	"log/slog"

	"github.com/merzzzl/openapi-mcp-server/internal/measure"
	"github.com/merzzzl/openapi-mcp-server/internal/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Service provides OpenAPI schema analysis and tool generation.
type Service struct {
	tracer  trace.Tracer
	logger  *slog.Logger
	metrics *measure.Metrics
	loader  repository.Loader
}

// New creates a schema Service with the given loader.
func New(loader repository.Loader) *Service {
	return &Service{
		tracer:  otel.Tracer("github.com/merzzzl/openapi-mcp-server/internal/service/schema"),
		logger:  slog.Default().With("component", "service.schema"),
		metrics: measure.Get(),
		loader:  loader,
	}
}
