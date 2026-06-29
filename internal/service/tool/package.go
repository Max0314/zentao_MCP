// Package tool provides tool execution against proxied APIs.
package tool

import (
	"log/slog"
	"regexp"

	"github.com/merzzzl/openapi-mcp-server/internal/measure"
	"github.com/merzzzl/openapi-mcp-server/internal/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Service executes API operations through a proxy.
type Service struct {
	tracer  trace.Tracer
	logger  *slog.Logger
	metrics *measure.Metrics
	proxy   repository.Proxy
	baseURL string
	rePath  *regexp.Regexp
}

// New creates a tool Service with the given proxy and base URL.
func New(proxy repository.Proxy, baseURL string) *Service {
	return &Service{
		tracer:  otel.Tracer("github.com/merzzzl/openapi-mcp-server/internal/service/tool"),
		logger:  slog.Default().With("component", "service.tool"),
		metrics: measure.Get(),
		proxy:   proxy,
		baseURL: baseURL,
		rePath:  regexp.MustCompile(`\{([^/}]+)\}|:([A-Za-z_][A-Za-z0-9_]*)`),
	}
}
