package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	healthPort            = 8080
	healthTimeout         = 5 * time.Second
	healthReadHeaderLimit = 5 * time.Second
)

// healthServer serves HTTP health check endpoints.
type healthServer struct {
	ready atomic.Bool
	srv   *http.Server
}

// newHealthServer creates an HTTP server on healthPort with /healthz endpoint.
func newHealthServer() *healthServer {
	h := &healthServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.handle)

	h.srv = &http.Server{
		Addr:              fmt.Sprintf(":%d", healthPort),
		Handler:           mux,
		ReadHeaderTimeout: healthReadHeaderLimit,
	}

	return h
}

// SetReady marks the service as ready to accept traffic.
func (h *healthServer) SetReady(ready bool) {
	h.ready.Store(ready)
}

// Start runs the HTTP server in background. Blocks until ctx is cancelled,
// then shuts down gracefully.
func (h *healthServer) Start(ctx context.Context) {
	go func() {
		if err := h.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(ctx, "health server error", slog.String("error", err.Error()))
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	if err := h.srv.Shutdown(shutdownCtx); err != nil {
		slog.ErrorContext(ctx, "health server shutdown error", slog.String("error", err.Error()))
	}
}

func (h *healthServer) handle(w http.ResponseWriter, _ *http.Request) {
	if h.ready.Load() {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")

		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprintln(w, "not ready")
}
