package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthorizationHandlerRequiresCredentials(t *testing.T) {
	called := false
	handler := NewAuthorizationHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/zentao-test/mcp", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("handler should not be called without credentials")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthorizationHandlerStoresZentaoCredentials(t *testing.T) {
	handler := NewAuthorizationHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		creds, ok := GetZentaoCredentials(r.Context())
		if !ok {
			t.Fatal("missing zentao credentials")
		}
		if creds.Account != "alice" || creds.Password != "secret" {
			t.Fatalf("credentials = %#v", creds)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/zentao-test/mcp", nil)
	req.Header.Set("zentao-account", "alice")
	req.Header.Set("zentao-password", "secret")
	req.Header.Set("token", "legacy")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestAuthorizationHandlerStoresLegacyToken(t *testing.T) {
	handler := NewAuthorizationHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token := GetAuthorization(r.Context()); token != "legacy" {
			t.Fatalf("token = %q, want legacy", token)
		}
		if !IsFromTokenHeader(r.Context()) {
			t.Fatal("legacy token should be marked as token header")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/zentao-test/mcp", nil)
	req.Header.Set("token", "legacy")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestAuthorizationHandlerRejectsPartialZentaoCredentials(t *testing.T) {
	handler := NewAuthorizationHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "/zentao-test/mcp", nil)
	req.Header.Set("zentao-account", "alice")
	req.Header.Set("token", "legacy")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
