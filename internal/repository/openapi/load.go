package openapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
)

var errUnexpectedStatus = errors.New("unexpected status")

// Load fetches and parses the OpenAPI specification.
func (r *Repository) Load(ctx context.Context) (*openapi3.T, error) {
	ctx, span := r.tracer.Start(ctx, "Load")
	defer span.End()

	r.logger.InfoContext(ctx, "loading openapi spec", "source", r.src)

	data, err := r.fetchData(ctx)
	if err != nil {
		return nil, err
	}

	if isSwaggerV2(data) {
		return r.loadV2(ctx, data)
	}

	return r.loadV3(ctx, data)
}

func (r *Repository) fetchData(ctx context.Context) ([]byte, error) {
	if strings.HasPrefix(r.src, "http://") || strings.HasPrefix(r.src, "https://") {
		return r.fetchHTTP(ctx)
	}

	data, err := os.ReadFile(r.src)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return data, nil
}

func (r *Repository) fetchHTTP(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.src, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d from %s", errUnexpectedStatus, resp.StatusCode, r.src)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return data, nil
}

func isSwaggerV2(data []byte) bool {
	var probe map[string]any

	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}

	_, hasSwagger := probe["swagger"]

	return hasSwagger
}

func (r *Repository) loadV2(ctx context.Context, data []byte) (*openapi3.T, error) {
	var specV2 openapi2.T

	if err := json.Unmarshal(data, &specV2); err != nil {
		return nil, fmt.Errorf("unmarshal swagger2: %w", err)
	}

	spec, err := openapi2conv.ToV3(&specV2)
	if err != nil {
		return nil, fmt.Errorf("convert swagger2 to openapi3: %w", err)
	}

	if err := spec.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validate openapi document: %w", err)
	}

	r.logger.InfoContext(ctx, "openapi document loaded", "version", "2")

	return spec, nil
}

func (r *Repository) loadV3(ctx context.Context, data []byte) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	spec, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("load openapi document: %w", err)
	}

	if err := spec.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validate openapi document: %w", err)
	}

	r.logger.InfoContext(ctx, "openapi document loaded", "version", "3")

	return spec, nil
}
