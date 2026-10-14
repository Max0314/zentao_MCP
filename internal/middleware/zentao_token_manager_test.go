package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func rejectingManager(t *testing.T, opts ZentaoAuthOptions) (*ZentaoTokenManager, *atomic.Int32) {
	t.Helper()
	attempts := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"incorrect credentials"}`))
	}))
	t.Cleanup(srv.Close)
	return NewZentaoTokenManager(srv.URL, srv.Client(), opts), attempts
}

func TestAccountBudgetCannotBeBypassedByChangingPasswords(t *testing.T) {
	m, attempts := rejectingManager(t, ZentaoAuthOptions{})
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	for i := range 2 {
		_, err := m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: strconv.Itoa(i)})
		var loginErr *ZentaoLoginError
		if !errors.As(err, &loginErr) || loginErr.StatusCode != 400 {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	now = now.Add(9 * time.Minute)
	_, err := m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: "third"})
	var loginErr *ZentaoLoginError
	if !errors.As(err, &loginErr) || loginErr.StatusCode != 429 || loginErr.RetryAfter != time.Minute {
		t.Fatalf("expected an account cooldown with one minute remaining, got %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatal("changing passwords bypassed the account budget")
	}
	// Suppressed requests do not extend the cooldown indefinitely.
	now = now.Add(2 * time.Minute)
	_, _ = m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: "fourth"})
	if attempts.Load() != 3 {
		t.Fatal("the account budget did not recover after the window")
	}
}

func TestWrongPasswordDoesNotEvictAWorkingSession(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		var c ZentaoCredentials
		_ = json.NewDecoder(r.Body).Decode(&c)
		if c.Password != "right" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"token":"working"}`))
	}))
	defer srv.Close()
	m := NewZentaoTokenManager(srv.URL, srv.Client())
	right := ZentaoCredentials{Account: "alice", Password: "right"}
	if _, err := m.Token(context.Background(), right); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"wrong1", "wrong2"} {
		_, _ = m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: password})
	}
	token, err := m.Token(context.Background(), right)
	if err != nil || token != "working" || attempts.Load() != 3 {
		t.Fatalf("valid cached session was disrupted: token=%q err=%v attempts=%d", token, err, attempts.Load())
	}
}

func TestSlowLoginDoesNotBlockAnotherAccountAndWaitersCanCancel(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c ZentaoCredentials
		_ = json.NewDecoder(r.Body).Decode(&c)
		if c.Account == "alice" {
			close(started)
			<-release
		}
		_, _ = w.Write([]byte(`{"token":"ok"}`))
	}))
	defer srv.Close()
	defer close(release)
	m := NewZentaoTokenManager(srv.URL, srv.Client())
	aliceDone := make(chan error, 1)
	go func() {
		_, err := m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: "right"})
		aliceDone <- err
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.Token(ctx, ZentaoCredentials{Account: "alice", Password: "right"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled waiter: %v", err)
	}
	bobCtx, bobCancel := context.WithTimeout(context.Background(), time.Second)
	defer bobCancel()
	if _, err := m.Token(bobCtx, ZentaoCredentials{Account: "bob", Password: "right"}); err != nil {
		t.Fatalf("alice's slow network login blocked bob: %v", err)
	}
}

func TestConcurrentDifferentPasswordsShareOneAccountBudget(t *testing.T) {
	m, attempts := rejectingManager(t, ZentaoAuthOptions{})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: strconv.Itoa(i)})
		}(i)
	}
	wg.Wait()
	if attempts.Load() != 2 {
		t.Fatalf("concurrent passwords bypassed the account budget: %d", attempts.Load())
	}
}

func TestCacheExpiryAndTokenCapacity(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		_, _ = w.Write([]byte(`{"token":"ok"}`))
	}))
	defer srv.Close()
	m := NewZentaoTokenManager(srv.URL, srv.Client(), ZentaoAuthOptions{TokenTTL: time.Minute, MaxCacheEntries: 2})
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	for _, account := range []string{"alice", "bob", "alice", "carol"} {
		if _, err := m.Token(context.Background(), ZentaoCredentials{Account: account, Password: "right"}); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	if len(m.tokens) != 2 || attempts.Load() != 3 {
		t.Fatalf("unbounded token cache or cache miss: %d / %d", len(m.tokens), attempts.Load())
	}
	if _, ok := m.tokens[m.cacheKey("bob")]; ok {
		t.Fatal("least recently used token was not evicted")
	}
	now = now.Add(2 * time.Minute)
	m.Cleanup()
	if len(m.tokens) != 0 {
		t.Fatal("expired tokens were retained")
	}
	if _, err := m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: "right"}); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 4 {
		t.Fatal("expired token was served without logging in")
	}
}

func TestProtectionCacheCapacityDoesNotEvictActiveBudgets(t *testing.T) {
	m, attempts := rejectingManager(t, ZentaoAuthOptions{MaxCacheEntries: 2})
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	for _, account := range []string{"alice", "bob", "carol", "alice", "alice"} {
		_, _ = m.Token(context.Background(), ZentaoCredentials{Account: account, Password: account + strconv.Itoa(int(attempts.Load()))})
	}
	if attempts.Load() != 3 || len(m.accountFailures) != 2 || len(m.failures) > 2 {
		t.Fatalf("active protection was evicted or a cache exceeded capacity: attempts=%d accounts=%d credentials=%d", attempts.Load(), len(m.accountFailures), len(m.failures))
	}
	now = now.Add(31 * time.Minute)
	m.Cleanup()
	if len(m.accountFailures) != 0 || len(m.failures) != 0 {
		t.Fatal("expired failure records were retained")
	}
}

func TestUpstreamServerFailuresDoNotConsumeTheAccountBudget(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	m := NewZentaoTokenManager(srv.URL, srv.Client())
	for i := range 4 {
		_, _ = m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: strconv.Itoa(i)})
	}
	if attempts.Load() != 4 || len(m.accountFailures) != 0 {
		t.Fatal("a server outage was counted as incorrect credentials")
	}
}

func TestThrottleResponseCarriesRetryAfterAndAuthFailureMarker(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bugs/1", nil)
	resp, err := loginErrorResponse(req, authThrottleError("cooldown", 1200*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "2" || resp.Header.Get(AuthFailureHeader) != "1" {
		t.Fatalf("incorrect throttle response: %d %v", resp.StatusCode, resp.Header)
	}
}

func TestAccountCooldownStartsAtTheLastActualRejection(t *testing.T) {
	m, attempts := rejectingManager(t, ZentaoAuthOptions{})
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	_, _ = m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: "one"})
	now = now.Add(9 * time.Minute)
	_, _ = m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: "two"})
	now = now.Add(time.Minute)
	_, err := m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: "three"})
	var loginErr *ZentaoLoginError
	if !errors.As(err, &loginErr) || loginErr.StatusCode != 429 || loginErr.RetryAfter != 9*time.Minute || attempts.Load() != 2 {
		t.Fatalf("cooldown expired relative to the first failure: err=%v attempts=%d", err, attempts.Load())
	}
}

func TestApplicationLevelLoginFailureAlsoConsumesBudget(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		_, _ = w.Write([]byte(`{"status":"fail","error":"incorrect credentials"}`))
	}))
	defer srv.Close()
	m := NewZentaoTokenManager(srv.URL, srv.Client())
	for i := range 3 {
		_, _ = m.Token(context.Background(), ZentaoCredentials{Account: "alice", Password: strconv.Itoa(i)})
	}
	if attempts.Load() != 2 {
		t.Fatal("HTTP 200 login rejection bypassed account protection")
	}
}
