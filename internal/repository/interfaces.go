// Package repository defines interfaces for data access.
package repository

import (
	"context"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
)

// Proxy sends HTTP requests to upstream services.
type Proxy interface {
	Do(ctx context.Context, req *http.Request) (*http.Response, error)
}

// Loader loads an OpenAPI specification.
type Loader interface {
	Load(ctx context.Context) (*openapi3.T, error)
}
