package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

func TestTransportLogsInCachesAndForwardsManagedToken(t *testing.T) {
	var mu sync.Mutex
	loginCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/v1/tokens":
			loginCount++
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode login payload: %v", err)
			}
			if payload["account"] != "alice" || payload["password"] != "secret" {
				http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "token-alice"})
		case "/api.php/v1/products":
			mu.Lock()
			defer mu.Unlock()
			if got := r.Header.Get("token"); got != "token-alice" {
				t.Fatalf("token header = %q, want token-alice", got)
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := WithZentaoAuth(server.Client(), server.URL+"/api.php/v1")
	ctx := WithZentaoCredentials(context.Background(), "alice", "secret")

	for i := 0; i < 2; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api.php/v1/products", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	}

	if loginCount != 1 {
		t.Fatalf("loginCount = %d, want 1", loginCount)
	}
}

func TestTransportFallsBackToUsersLoginWhenTokensEndpointIsMissing(t *testing.T) {
	loginCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/v1/tokens":
			http.NotFound(w, r)
		case "/api.php/v1/users/login":
			loginCount++
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "token-alice"})
		case "/api.php/v1/products":
			if got := r.Header.Get("token"); got != "token-alice" {
				t.Fatalf("token header = %q, want token-alice", got)
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := WithZentaoAuth(server.Client(), server.URL+"/api.php/v1")
	req, err := http.NewRequestWithContext(WithZentaoCredentials(context.Background(), "alice", "secret"), http.MethodGet, server.URL+"/api.php/v1/products", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if loginCount != 1 {
		t.Fatalf("loginCount = %d, want 1", loginCount)
	}
}

func TestTransportIsolatesManagedTokensByAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/v1/tokens":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode login payload: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "token-" + payload["account"]})
		case "/api.php/v1/products":
			account := r.URL.Query().Get("account")
			if got, want := r.Header.Get("token"), "token-"+account; got != want {
				t.Fatalf("token header = %q, want %q", got, want)
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := WithZentaoAuth(server.Client(), server.URL+"/api.php/v1")

	for _, account := range []string{"alice", "bob"} {
		ctx := WithZentaoCredentials(context.Background(), account, "secret")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api.php/v1/products?account="+account, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	}
}

func TestTransportDoesNotReuseCachedTokenForDifferentPassword(t *testing.T) {
	loginCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/v1/tokens":
			loginCount++
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode login payload: %v", err)
			}
			if payload["password"] != "secret" {
				http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "token-alice"})
		case "/api.php/v1/products":
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := WithZentaoAuth(server.Client(), server.URL+"/api.php/v1")

	req, err := http.NewRequestWithContext(WithZentaoCredentials(context.Background(), "alice", "secret"), http.MethodGet, server.URL+"/api.php/v1/products", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	req, err = http.NewRequestWithContext(WithZentaoCredentials(context.Background(), "alice", "wrong"), http.MethodGet, server.URL+"/api.php/v1/products", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if loginCount != 2 {
		t.Fatalf("loginCount = %d, want 2", loginCount)
	}
}

func TestTransportRefreshesManagedTokenAfterUnauthorized(t *testing.T) {
	loginCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/v1/tokens":
			loginCount++
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "token-" + strconv.Itoa(loginCount)})
		case "/api.php/v1/products":
			if got := r.Header.Get("token"); got == "token-1" {
				http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if got := r.Header.Get("token"); got != "token-2" {
				t.Fatalf("token header = %q, want token-2", got)
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := WithZentaoAuth(server.Client(), server.URL+"/api.php/v1")
	req, err := http.NewRequestWithContext(WithZentaoCredentials(context.Background(), "alice", "secret"), http.MethodGet, server.URL+"/api.php/v1/products", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if loginCount != 2 {
		t.Fatalf("loginCount = %d, want 2", loginCount)
	}
}

func TestTransportForwardsLegacyTokenWithoutLogin(t *testing.T) {
	loginCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/v1/tokens":
			loginCalled = true
			t.Fatal("login should not be called for legacy token")
		case "/api.php/v1/products":
			if got := r.Header.Get("token"); got != "legacy" {
				t.Fatalf("token header = %q, want legacy", got)
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := WithZentaoAuth(server.Client(), server.URL+"/api.php/v1")
	ctx := WithAuthorization(context.Background(), "legacy")
	ctx = WithAuthorizationSource(ctx, true)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api.php/v1/products", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if loginCalled {
		t.Fatal("login was called")
	}
}

func TestTransportPrefersManagedCredentialsOverLegacyToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/v1/tokens":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "managed"})
		case "/api.php/v1/products":
			if got := r.Header.Get("token"); got != "managed" {
				t.Fatalf("token header = %q, want managed", got)
			}
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := WithZentaoAuth(server.Client(), server.URL+"/api.php/v1")
	ctx := WithZentaoCredentials(context.Background(), "alice", "secret")
	ctx = WithAuthorization(ctx, "legacy")
	ctx = WithAuthorizationSource(ctx, true)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api.php/v1/products", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
