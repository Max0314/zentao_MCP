package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func authTestUpstream(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	logins, profiles := new(atomic.Int32), new(atomic.Int32)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tokens":
			logins.Add(1)
			var c map[string]string
			_ = json.NewDecoder(r.Body).Decode(&c)
			if c["password"] != "good" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"debug-secret-password"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"valid-token"}`))
		case "/user":
			profiles.Add(1)
			if r.Header.Get("Token") != "valid-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"profile":{"account":"alice"}}`))
		case "/products":
			if r.Header.Get("Token") != "valid-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"products":[],"total":0}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s, logins, profiles
}

func TestZentaoGateRejectsBeforeProtocolHandler(t *testing.T) {
	s, logins, profiles := authTestUpstream(t)
	_, manager := NewZentaoAuthClient(s.Client(), s.URL)
	called := 0
	h := NewZentaoAuthenticator(manager, ZentaoAccessOptions{}).Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))
	for _, headers := range []map[string]string{
		{},
		{"zentao-account": "alice"},
		{"zentao-password": "good", "token": "valid-token"},
		{"zentao-account": "alice", "zentao-password": "bad"},
		{"token": "invalid-token"},
		{"Authorization": "Bearer invalid-token"},
	} {
		for _, route := range []string{"/zentao/mcp", "/zentao/sse?sessionid=untrusted"} {
			r := httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			for k, v := range headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized || called != 0 || w.Header().Get(AuthFailureHeader) != "1" {
				t.Fatalf("invalid credentials reached MCP: status=%d called=%d", w.Code, called)
			}
			if strings.Contains(w.Body.String(), "debug-secret") {
				t.Fatal("upstream login body leaked")
			}
		}
	}
	if logins.Load() != 1 || profiles.Load() != 4 {
		t.Fatalf("unexpected upstream attempts: logins=%d profiles=%d", logins.Load(), profiles.Load())
	}
}

func TestZentaoGateSharesTransportAndIndexLogin(t *testing.T) {
	s, logins, _ := authTestUpstream(t)
	client, manager := NewZentaoAuthClient(s.Client(), s.URL)
	ctx := WithZentaoCredentials(context.Background(), "alice", "good")
	// The index/business transport authenticates first; the gate must reuse it.
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/products", nil)
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	a := NewZentaoAuthenticator(manager, ZentaoAccessOptions{IPLoginBurst: 1, LoginBurst: 1})
	called := 0
	h := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, s.URL+"/products", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		_ = resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	}))
	for _, route := range []string{"/zentao/mcp", "/zentao/mcp", "/zentao/sse"} {
		r := httptest.NewRequest(http.MethodPost, route, nil)
		r.Header.Set("zentao-account", "alice")
		r.Header.Set("zentao-password", "good")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("cached session rejected: %d", w.Code)
		}
	}
	if called != 3 || logins.Load() != 1 {
		t.Fatalf("entry and transport did not share login: called=%d logins=%d", called, logins.Load())
	}
}

func TestZentaoGateValidatesAndNormalizesLegacyTokens(t *testing.T) {
	s, logins, profiles := authTestUpstream(t)
	client, manager := NewZentaoAuthClient(s.Client(), s.URL)
	h := NewZentaoAuthenticator(manager, ZentaoAccessOptions{}).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, s.URL+"/products", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		_ = resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	}))
	for _, headers := range []map[string]string{{"token": "valid-token"}, {"Authorization": "Bearer valid-token"}} {
		r := httptest.NewRequest(http.MethodPost, "/zentao/mcp", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("valid token rejected: %d", w.Code)
		}
	}
	if logins.Load() != 0 || profiles.Load() != 2 {
		t.Fatal("token mode attempted password login or skipped profile verification")
	}
}

func TestZentaoGateLimitsNewLoginsButAllowsCachedSession(t *testing.T) {
	s, logins, _ := authTestUpstream(t)
	_, manager := NewZentaoAuthClient(s.Client(), s.URL)
	h := NewZentaoAuthenticator(manager, ZentaoAccessOptions{IPLoginRate: 0.001, IPLoginBurst: 1}).Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for i, account := range []string{"alice", "bob", "alice"} {
		r := httptest.NewRequest(http.MethodPost, "/zentao/mcp", nil)
		r.Header.Set("zentao-account", account)
		r.Header.Set("zentao-password", "good")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := http.StatusNoContent
		if i == 1 {
			want = http.StatusTooManyRequests
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("missing retry hint")
			}
		}
		if w.Code != want {
			t.Fatalf("attempt %d: status=%d want=%d", i, w.Code, want)
		}
	}
	if logins.Load() != 1 {
		t.Fatal("rate-limited login reached upstream")
	}
}

func TestZentaoGateFailsClosedOnUpstreamOutage(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer s.Close()
	_, manager := NewZentaoAuthClient(s.Client(), s.URL)
	h := NewZentaoAuthenticator(manager, ZentaoAccessOptions{}).Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unverified request reached protocol") }))
	for _, headers := range []map[string]string{{"zentao-account": "alice", "zentao-password": "good"}, {"token": "valid-token"}} {
		r := httptest.NewRequest(http.MethodPost, "/zentao/mcp", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("outage status=%d", w.Code)
		}
	}
}
