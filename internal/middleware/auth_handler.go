package middleware

import (
	"log/slog"
	"net/http"
)

// AuthorizationHandler extracts auth information from headers and stores it in the context.
type AuthorizationHandler struct {
	handler http.Handler
}

// NewAuthorizationHandler wraps an http.Handler with authorization extraction.
func NewAuthorizationHandler(handler http.Handler) http.Handler {
	return &AuthorizationHandler{handler: handler}
}

func (h *AuthorizationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	account := r.Header.Get("zentao-account")
	password := r.Header.Get("zentao-password")

	if account != "" || password != "" {
		if account == "" || password == "" {
			slog.WarnContext(r.Context(), "incomplete zentao credentials")
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		r = r.WithContext(WithZentaoCredentials(r.Context(), account, password))
		h.handler.ServeHTTP(w, r)

		return
	}

	if token := r.Header.Get("token"); token != "" {
		ctx := WithAuthorization(r.Context(), token)
		ctx = WithAuthorizationSource(ctx, true)
		r = r.WithContext(ctx)

		h.handler.ServeHTTP(w, r)

		return
	}

	auth := r.Header.Get("Authorization")
	if auth == "" {
		slog.WarnContext(r.Context(), "missing authorization header")
		w.WriteHeader(http.StatusUnauthorized)

		return
	}

	ctx := WithAuthorization(r.Context(), auth)
	ctx = WithAuthorizationSource(ctx, false)
	r = r.WithContext(ctx)

	h.handler.ServeHTTP(w, r)
}
