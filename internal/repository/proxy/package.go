// Package proxy implements HTTP request proxying.
package proxy

import (
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Repository proxies HTTP requests to upstream services.
type Repository struct {
	tracer trace.Tracer
	logger *slog.Logger
	client *http.Client
}

// New creates a proxy Repository with the given HTTP client.
func New(client *http.Client) *Repository {
	return &Repository{
		tracer: otel.Tracer("github.com/merzzzl/openapi-mcp-server/internal/repository/proxy"),
		logger: slog.Default().With(
			"component", "repository.proxy",
		),
		client: client,
	}
}
