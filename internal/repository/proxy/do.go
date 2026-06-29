package proxy

import (
	"context"
	"fmt"
	"net/http"
)

// Do sends an HTTP request through the proxy.
func (r *Repository) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	ctx, span := r.tracer.Start(ctx, "Do")
	defer span.End()

	r.logger.InfoContext(ctx, "proxying request",
		"method", req.Method,
		"url", req.URL.String(),
	)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	return resp, nil
}
