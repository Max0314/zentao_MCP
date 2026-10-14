package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const maxLoginResponseSize = 1 << 20

// Backoff after a rejected login. ZenTao locks an account after a handful of
// failed attempts, and every further attempt restarts its lock timer, so
// retrying on each request keeps a locked account locked. Failures are cached
// and the wait doubles, giving the lock time to expire on its own.
const (
	loginBackoffBase = 15 * time.Second
	loginBackoffMax  = 10 * time.Minute
	loginTimeout     = 30 * time.Second
)

// ZentaoAuthOptions bounds cached credentials and failed upstream logins.
// FailureLimit defaults to two: a third rejected login can lock a ZenTao
// account. Deployments with a different policy can tune the window and limit.
type ZentaoAuthOptions struct {
	FailureLimit    int
	FailureWindow   time.Duration
	TokenTTL        time.Duration
	FailureTTL      time.Duration
	MaxCacheEntries int
}

func (o ZentaoAuthOptions) normalized() ZentaoAuthOptions {
	if o.FailureLimit <= 0 {
		o.FailureLimit = 2
	}
	if o.FailureWindow <= 0 {
		o.FailureWindow = 10 * time.Minute
	}
	if o.TokenTTL <= 0 {
		o.TokenTTL = 24 * time.Hour
	}
	if o.FailureTTL <= 0 {
		o.FailureTTL = 30 * time.Minute
	}
	if o.MaxCacheEntries <= 0 {
		o.MaxCacheEntries = 2048
	}
	return o
}

type tokenEntry struct {
	token        string
	passwordHash [32]byte
	expiresAt    time.Time
	lastUsed     time.Time
}

// failureEntry suppresses login attempts for an account that was just rejected.
type failureEntry struct {
	err       error
	until     time.Time
	count     int
	expiresAt time.Time
}

type accountFailureEntry struct {
	count int
	until time.Time
}

// ZentaoLoginError represents a non-successful Zentao login response.
type ZentaoLoginError struct {
	StatusCode int
	Body       []byte
	RetryAfter time.Duration
}

func (e *ZentaoLoginError) Error() string {
	return fmt.Sprintf("zentao login failed with HTTP %d", e.StatusCode)
}

// ZentaoTokenManager logs into Zentao and caches user tokens in memory.
type ZentaoTokenManager struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger

	mu              sync.Mutex
	tokens          map[string]tokenEntry
	failures        map[string]failureEntry
	accountFailures map[string]accountFailureEntry
	inflight        map[string]chan struct{}
	opts            ZentaoAuthOptions
	now             func() time.Time
}

// NewZentaoTokenManager creates a token manager for one upstream Zentao base URL.
func NewZentaoTokenManager(baseURL string, client *http.Client, options ...ZentaoAuthOptions) *ZentaoTokenManager {
	if client == nil {
		client = http.DefaultClient
	}

	var opts ZentaoAuthOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return &ZentaoTokenManager{
		baseURL:         baseURL,
		client:          client,
		logger:          slog.Default().With("component", "middleware.zentao_auth"),
		tokens:          make(map[string]tokenEntry),
		failures:        make(map[string]failureEntry),
		accountFailures: make(map[string]accountFailureEntry),
		inflight:        make(map[string]chan struct{}),
		opts:            opts.normalized(),
		now:             time.Now,
	}
}

// Token returns a cached token for the account or logs in when no matching token exists.
func (m *ZentaoTokenManager) Token(ctx context.Context, creds ZentaoCredentials) (string, error) {
	return m.token(ctx, creds, "", nil)
}

// TokenWithLoginGuard applies a cheap guard only when a new upstream login is
// needed. Cache hits and locally suppressed failures do not consume its budget.
func (m *ZentaoTokenManager) TokenWithLoginGuard(ctx context.Context, creds ZentaoCredentials, guard func() error) (string, error) {
	return m.token(ctx, creds, "", guard)
}

// Refresh exchanges a token that the upstream rejected for a fresh one. Pass
// the token that produced the 401: if another caller has already replaced it,
// that replacement is returned instead of logging in again. Without this,
// concurrent 401s each perform their own login and a burst of them locks the
// ZenTao account.
func (m *ZentaoTokenManager) Refresh(ctx context.Context, creds ZentaoCredentials, stale string) (string, error) {
	return m.token(ctx, creds, stale, nil)
}

// token returns a usable token. stale is empty for an ordinary lookup, or the
// rejected token when refreshing.
func (m *ZentaoTokenManager) token(ctx context.Context, creds ZentaoCredentials, stale string, guard func() error) (string, error) {
	hash := sha256.Sum256([]byte(creds.Password))
	key := m.cacheKey(creds.Account)
	// Keep token caches case-sensitive, but serialize and protect account-name
	// variants together. Otherwise changing case or whitespace can bypass the
	// upstream lockout protection without changing the actual ZenTao account.
	protectionKey := m.cacheKey(strings.ToLower(strings.TrimSpace(creds.Account)))
	fkey := failureKey(key, hash)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		m.mu.Lock()
		now := m.now()
		m.cleanupLocked(now)
		// Existing valid sessions remain usable even if another client has
		// exhausted this account's login budget with incorrect credentials.
		if entry, ok := m.tokens[key]; ok && entry.passwordHash == hash && entry.token != stale {
			entry.lastUsed = now
			m.tokens[key] = entry
			m.mu.Unlock()
			return entry.token, nil
		}
		if f, ok := m.accountFailures[protectionKey]; ok && f.count >= m.opts.FailureLimit {
			m.mu.Unlock()
			return "", authThrottleError("该账号登录失败次数达到保护上限，请检查正式/测试环境和密码，冷却后再试", f.until.Sub(now))
		}
		if f, ok := m.failures[fkey]; ok && now.Before(f.until) {
			m.mu.Unlock()
			return "", f.err
		}
		// Coalesce by account, including callers supplying different passwords.
		// Waiters can cancel and other accounts never wait on a network login.
		if done, ok := m.inflight[protectionKey]; ok {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-done:
				continue
			}
		}
		_, protected := m.accountFailures[protectionKey]
		if !protected {
			// Never evict an active protection bucket: eviction would reset its
			// budget and let a flood of new account names bypass the guard.
			if len(m.accountFailures) >= m.opts.MaxCacheEntries {
				wait := m.opts.FailureWindow
				for _, f := range m.accountFailures {
					if d := f.until.Sub(now); d < wait {
						wait = d
					}
				}
				m.mu.Unlock()
				return "", authThrottleError("登录保护缓存已满，请稍后重试", wait)
			}
		}
		if guard != nil {
			if err := guard(); err != nil {
				m.mu.Unlock()
				return "", err
			}
		}
		if !protected {
			m.accountFailures[protectionKey] = accountFailureEntry{until: now.Add(m.opts.FailureWindow)}
		}
		done := make(chan struct{})
		m.inflight[protectionKey] = done
		m.mu.Unlock()

		loginCtx, cancel := context.WithTimeout(ctx, loginTimeout)
		token, err := m.login(loginCtx, creds)
		cancel()

		m.mu.Lock()
		now = m.now()
		if err != nil {
			// A wrong password must not evict a working token for this account.
			if entry, ok := m.tokens[key]; ok && entry.passwordHash == hash {
				delete(m.tokens, key)
			}
			if ctx.Err() == nil {
				m.recordFailure(ctx, fkey, creds.Account, err, now)
			}
			var rejected *ZentaoLoginError
			if errors.As(err, &rejected) && (rejected.StatusCode == 400 || rejected.StatusCode == 401 || rejected.StatusCode == 403) {
				f := m.accountFailures[protectionKey]
				if !now.Before(f.until) {
					f = accountFailureEntry{until: now.Add(m.opts.FailureWindow)}
				}
				f.count++
				// Measure the quiet period from the last real rejected login.
				// Locally suppressed requests never move this deadline.
				f.until = now.Add(m.opts.FailureWindow)
				m.accountFailures[protectionKey] = f
				if f.count >= m.opts.FailureLimit {
					m.logger.WarnContext(ctx, "zentao account login protection activated",
						"account", creds.Account, "failures", f.count, "retry_in", m.opts.FailureWindow.String())
				}
			} else if m.accountFailures[protectionKey].count == 0 {
				delete(m.accountFailures, protectionKey)
			}
		} else {
			delete(m.accountFailures, protectionKey)
			for k := range m.failures {
				if strings.HasPrefix(k, key+"#") {
					delete(m.failures, k)
				}
			}
			m.makeTokenRoomLocked(key)
			m.tokens[key] = tokenEntry{token: token, passwordHash: hash, expiresAt: now.Add(m.opts.TokenTTL), lastUsed: now}
		}
		delete(m.inflight, protectionKey)
		close(done)
		m.mu.Unlock()
		return token, err
	}
}

// recordFailure holds off further logins for this account, doubling the wait
// on each consecutive rejection.
func (m *ZentaoTokenManager) recordFailure(ctx context.Context, key, account string, err error, now time.Time) {
	prev := m.failures[key]

	wait := loginBackoffBase << min(prev.count, 6)
	if wait > loginBackoffMax {
		wait = loginBackoffMax
	}

	if _, exists := m.failures[key]; !exists && len(m.failures) >= m.opts.MaxCacheEntries {
		var oldest string
		for k, f := range m.failures {
			if oldest == "" || f.expiresAt.Before(m.failures[oldest].expiresAt) {
				oldest = k
			}
		}
		delete(m.failures, oldest)
	}
	m.failures[key] = failureEntry{err: err, until: now.Add(wait), count: prev.count + 1, expiresAt: now.Add(max(m.opts.FailureTTL, wait))}

	m.logger.WarnContext(ctx, "zentao login failed, backing off",
		"account", account,
		"consecutive_failures", prev.count+1,
		"retry_in", wait.String(),
	)
}

// Cleanup expires idle protection and cache entries. It also runs on lookup,
// so no background goroutine or timer needs to survive a client's lifetime.
func (m *ZentaoTokenManager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(m.now())
}

func (m *ZentaoTokenManager) cleanupLocked(now time.Time) {
	for k, t := range m.tokens {
		if !now.Before(t.expiresAt) {
			delete(m.tokens, k)
		}
	}
	for k, f := range m.failures {
		if !now.Before(f.expiresAt) {
			delete(m.failures, k)
		}
	}
	for k, f := range m.accountFailures {
		if _, busy := m.inflight[k]; !busy && !now.Before(f.until) {
			delete(m.accountFailures, k)
		}
	}
}

func (m *ZentaoTokenManager) makeTokenRoomLocked(key string) {
	if _, exists := m.tokens[key]; exists || len(m.tokens) < m.opts.MaxCacheEntries {
		return
	}
	var oldest string
	for k, t := range m.tokens {
		if oldest == "" || t.lastUsed.Before(m.tokens[oldest].lastUsed) {
			oldest = k
		}
	}
	delete(m.tokens, oldest)
}

func authThrottleError(message string, wait time.Duration) *ZentaoLoginError {
	seconds := max(1, int((wait+time.Second-1)/time.Second))
	body, _ := json.Marshal(map[string]any{"error": message, "retryAfterSeconds": seconds})
	return &ZentaoLoginError{StatusCode: http.StatusTooManyRequests, Body: body, RetryAfter: time.Duration(seconds) * time.Second}
}

// failureKey scopes a login backoff to one account-and-password pair.
func failureKey(cacheKey string, hash [32]byte) string {
	return cacheKey + "#" + hex.EncodeToString(hash[:])
}

func (m *ZentaoTokenManager) cacheKey(account string) string {
	return m.baseURL + "\x00" + account
}

func (m *ZentaoTokenManager) login(ctx context.Context, creds ZentaoCredentials) (string, error) {
	var lastErr error
	for _, endpoint := range []string{"tokens", "users/login"} {
		token, err := m.loginAt(ctx, creds, endpoint)
		if err == nil {
			return token, nil
		}

		lastErr = err

		var loginErr *ZentaoLoginError
		if !errors.As(err, &loginErr) {
			return "", err
		}
		if loginErr.StatusCode != http.StatusNotFound && loginErr.StatusCode != http.StatusMethodNotAllowed {
			return "", err
		}
	}

	return "", lastErr
}

func (m *ZentaoTokenManager) loginAt(ctx context.Context, creds ZentaoCredentials, endpoint string) (string, error) {
	u, err := url.Parse(m.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}

	u.Path = path.Join(u.Path, endpoint)

	body, err := json.Marshal(map[string]string{
		"account":  creds.Account,
		"password": creds.Password,
	})
	if err != nil {
		return "", fmt.Errorf("marshal login payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create login request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("do login request: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxLoginResponseSize))
	if err != nil {
		return "", fmt.Errorf("read login response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		m.logger.WarnContext(ctx, "zentao login rejected",
			"account", creds.Account,
			"endpoint", endpoint,
			"status", resp.StatusCode,
		)

		// This response is retained in the failure cache. Bound each entry as
		// well as the number of entries; an upstream HTML error can be large.
		if len(respBody) > 4096 {
			respBody, _ = json.Marshal(map[string]any{"error": "禅道登录失败，过长的响应内容已省略", "status": resp.StatusCode})
		}
		return "", &ZentaoLoginError{StatusCode: resp.StatusCode, Body: respBody}
	}

	var data struct {
		Status string          `json:"status"`
		Token  string          `json:"token"`
		Error  json.RawMessage `json:"error"`
	}

	if err := json.Unmarshal(respBody, &data); err != nil {
		return "", fmt.Errorf("parse login response: %w", err)
	}
	status := strings.ToLower(data.Status)
	if status == "fail" || status == "failed" || status == "error" ||
		(len(data.Error) > 0 && string(data.Error) != "null" && string(data.Error) != `""` && string(data.Error) != "false") {
		if len(respBody) > 4096 {
			respBody = []byte(`{"error":"禅道登录被拒绝，过长的响应内容已省略"}`)
		}
		return "", &ZentaoLoginError{StatusCode: http.StatusUnauthorized, Body: respBody}
	}

	if data.Token == "" {
		return "", fmt.Errorf("login response missing token")
	}

	m.logger.InfoContext(ctx, "zentao login succeeded", "account", creds.Account, "endpoint", endpoint)

	return data.Token, nil
}
