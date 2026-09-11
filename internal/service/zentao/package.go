// Package zentao provides composite ZenTao operations that are not exposed as
// single OpenAPI endpoints, such as cross-scope bug search, AI score reading
// and problem analysis.
package zentao

import (
	"log/slog"

	"github.com/merzzzl/openapi-mcp-server/internal/repository"
	"github.com/merzzzl/openapi-mcp-server/internal/service/bugindex"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Service aggregates several ZenTao v1 read operations into higher level answers.
type Service struct {
	tracer  trace.Tracer
	logger  *slog.Logger
	proxy   repository.Proxy
	baseURL string
	// index is the optional historical bug corpus. It is built under a
	// service account and is only ever used to rank candidates; results are
	// re-read as the calling user before anything is returned.
	index     *bugindex.Index
	indexOpts IndexOptions
}

// New creates a ZenTao composite Service on top of the shared proxy repository.
func New(proxy repository.Proxy, baseURL string) *Service {
	return &Service{
		tracer:  otel.Tracer("github.com/merzzzl/openapi-mcp-server/internal/service/zentao"),
		logger:  slog.Default().With("component", "service.zentao"),
		proxy:   proxy,
		baseURL: baseURL,
	}
}
