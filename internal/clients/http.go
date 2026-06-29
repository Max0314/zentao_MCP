// Package clients provides HTTP client constructors.
package clients

import (
	"net/http"

	"github.com/merzzzl/openapi-mcp-server/internal/middleware"
)

// NewHTTPClient creates an HTTP client with Zentao auth transport.
func NewHTTPClient(base *http.Client, baseURL string) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}

	return middleware.WithZentaoAuth(base, baseURL)
}
