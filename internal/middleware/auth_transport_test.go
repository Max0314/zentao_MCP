package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
	"sync/atomic"
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

// A ZenTao token expires while the server is running; the next call must be
// able to re-login and replay. Bodyless GETs are replayable however the caller
// spelled "no body", so an expired token never turns into a user-visible 401.
func TestCanReplayBodylessRequests(t *testing.T) {
	tests := []struct {
		name string
		body io.Reader
	}{
		{name: "nil body", body: nil},
		{name: "http.NoBody", body: http.NoBody},
	}

	for _, tt := range tests {
		req, err := http.NewRequest(http.MethodGet, "http://zentao.test/api.php/v1/products", tt.body)
		if err != nil {
			t.Fatalf("%s: new request: %v", tt.name, err)
		}

		if !canReplay(req) {
			t.Fatalf("%s: canReplay = false, so a 401 would be returned without refreshing the token", tt.name)
		}
	}
}

func TestCanReplayRejectsUnrewindableBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://zentao.test/api.php/v1/bugs", io.LimitReader(strings.NewReader("payload"), 7))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.GetBody = nil

	if canReplay(req) {
		t.Fatal("a body that cannot be rewound must not be replayed")
	}
}

// A burst of concurrent 401s must produce exactly one login. Without this,
// verifyHits' parallel reads each refreshed on their own and the resulting
// login storm locked the user's ZenTao account.
func TestConcurrentRefreshPerformsOneLogin(t *testing.T) {
	var logins int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&logins, 1)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "fresh-" + strconv.Itoa(int(atomic.LoadInt32(&logins)))})
	}))
	defer srv.Close()

	m := NewZentaoTokenManager(srv.URL, srv.Client())
	creds := ZentaoCredentials{Account: "someone", Password: "secret"}

	first, err := m.Token(context.Background(), creds)
	if err != nil {
		t.Fatalf("initial token: %v", err)
	}

	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if _, err := m.Refresh(context.Background(), creds, first); err != nil {
				t.Errorf("refresh: %v", err)
			}
		}()
	}

	wg.Wait()

	// One login for the initial token, one for the whole refresh burst.
	if got := atomic.LoadInt32(&logins); got != 2 {
		t.Fatalf("logins = %d, want 2 (a burst of 401s must coalesce into one login)", got)
	}
}

// A rejected login must not be retried on the next request: ZenTao restarts its
// lock timer on every failed attempt, so retrying keeps a locked account locked.
func TestRejectedLoginBacksOff(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"您还有2次尝试机会。"}`))
	}))
	defer srv.Close()

	m := NewZentaoTokenManager(srv.URL, srv.Client())
	creds := ZentaoCredentials{Account: "someone", Password: "wrong"}

	for range 5 {
		if _, err := m.Token(context.Background(), creds); err == nil {
			t.Fatal("expected the login to fail")
		}
	}

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("login attempts = %d, want 1; further attempts keep the account locked", got)
	}

	// The backoff must expire rather than wedge the account out permanently.
	m.mu.Lock()
	m.now = func() time.Time { return time.Now().Add(2 * loginBackoffMax) }
	m.mu.Unlock()

	if _, err := m.Token(context.Background(), creds); err == nil {
		t.Fatal("expected the login to fail")
	}

	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("login attempts after the backoff = %d, want 2", got)
	}
}

// Correcting a mistyped password must take effect at once. The backoff is
// keyed by credential, so the penalty earned by the wrong password does not
// hold back the right one -- a user who fixed their config should not have to
// wait out a ten minute wait they did not cause.
func TestCorrectedPasswordIsNotHeldBackByTheBackoff(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode login payload: %v", err)
		}

		if payload["password"] != "right" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"登录失败，请检查用户名或密码是否填写正确。"}`))

			return
		}

		_ = json.NewEncoder(w).Encode(map[string]string{"token": "token-ok"})
	}))
	defer srv.Close()

	m := NewZentaoTokenManager(srv.URL, srv.Client())

	// Two rejected attempts with the wrong password put that credential into backoff.
	for range 2 {
		if _, err := m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: "wrong"}); err == nil {
			t.Fatal("expected the wrong password to be rejected")
		}
	}

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts with the wrong password = %d, want 1 (the second must be suppressed)", got)
	}

	// The corrected password must be tried immediately, not suppressed.
	token, err := m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: "right"})
	if err != nil {
		t.Fatalf("the corrected password was blocked by the wrong password's backoff: %v", err)
	}

	if token != "token-ok" {
		t.Fatalf("token = %q, want token-ok", token)
	}

	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2 (one wrong, one corrected)", got)
	}
}
