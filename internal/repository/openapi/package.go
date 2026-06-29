// Package openapi implements OpenAPI specification loading.
package openapi

import (
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Repository loads OpenAPI specifications from remote or local sources.
type Repository struct {
	tracer trace.Tracer
	logger *slog.Logger
	client *http.Client
	src    string
}

// New creates a Repository for the given source URL or file path.
func New(client *http.Client, src string) *Repository {
	return &Repository{
		tracer: otel.Tracer("github.com/merzzzl/openapi-mcp-server/internal/repository/openapi"),
		logger: slog.Default().With(
			"component", "repository.openapi",
		),
		client: client,
		src:    src,
	}
}
