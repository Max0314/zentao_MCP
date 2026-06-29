// Package measure provides application-level OpenTelemetry metric instruments.
package measure

import (
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	api "go.opentelemetry.io/otel/metric"
)

const meterName = "github.com/merzzzl/openapi-mcp-server"

var (
	toolDurationBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	httpDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
	global              *Metrics
	once                sync.Once
)

// Metrics holds all application metric instruments.
type Metrics struct {
	ToolCalls    api.Int64Counter
	ToolDuration api.Float64Histogram
	SchemaLoads  api.Int64Counter
	HTTPRequests api.Int64Counter
	HTTPDuration api.Float64Histogram
}

// Get returns the global Metrics instance, creating it on first call.
func Get() *Metrics {
	once.Do(func() {
		meter := otel.Meter(meterName)

		global = &Metrics{
			ToolCalls:    must(meter.Int64Counter("mcp.tool.calls", api.WithDescription("Total number of MCP tool calls"), api.WithUnit("{call}"))),
			ToolDuration: must(meter.Float64Histogram("mcp.tool.duration", api.WithDescription("Duration of MCP tool calls"), api.WithUnit("s"), api.WithExplicitBucketBoundaries(toolDurationBuckets...))),
			SchemaLoads:  must(meter.Int64Counter("mcp.schema.loads", api.WithDescription("Total number of OpenAPI schema load operations"), api.WithUnit("{call}"))),
			HTTPRequests: must(meter.Int64Counter("mcp.http.requests", api.WithDescription("Total number of outgoing HTTP requests"), api.WithUnit("{request}"))),
			HTTPDuration: must(meter.Float64Histogram("mcp.http.duration", api.WithDescription("Duration of outgoing HTTP requests"), api.WithUnit("s"), api.WithExplicitBucketBoundaries(httpDurationBuckets...))),
		}
	})

	return global
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("measure: %v", err))
	}

	return v
}
