package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
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
)

type tokenEntry struct {
	token        string
	passwordHash [32]byte
}

// failureEntry suppresses login attempts for an account that was just rejected.
type failureEntry struct {
	err   error
	until time.Time
	count int
}

// ZentaoLoginError represents a non-successful Zentao login response.
type ZentaoLoginError struct {
	StatusCode int
	Body       []byte
}

func (e *ZentaoLoginError) Error() string {
	return fmt.Sprintf("zentao login failed with HTTP %d", e.StatusCode)
}

// ZentaoTokenManager logs into Zentao and caches user tokens in memory.
type ZentaoTokenManager struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger

	mu       sync.Mutex
	tokens   map[string]tokenEntry
	failures map[string]failureEntry
	now      func() time.Time
}

// NewZentaoTokenManager creates a token manager for one upstream Zentao base URL.
func NewZentaoTokenManager(baseURL string, client *http.Client) *ZentaoTokenManager {
	if client == nil {
		client = http.DefaultClient
	}

	return &ZentaoTokenManager{
		baseURL:  baseURL,
		client:   client,
		logger:   slog.Default().With("component", "middleware.zentao_auth"),
		tokens:   make(map[string]tokenEntry),
		failures: make(map[string]failureEntry),
		now:      time.Now,
	}
}

// Token returns a cached token for the account or logs in when no matching token exists.
func (m *ZentaoTokenManager) Token(ctx context.Context, creds ZentaoCredentials) (string, error) {
	return m.token(ctx, creds, "")
}

// Refresh exchanges a token that the upstream rejected for a fresh one. Pass
// the token that produced the 401: if another caller has already replaced it,
// that replacement is returned instead of logging in again. Without this,
// concurrent 401s each perform their own login and a burst of them locks the
// ZenTao account.
func (m *ZentaoTokenManager) Refresh(ctx context.Context, creds ZentaoCredentials, stale string) (string, error) {
	return m.token(ctx, creds, stale)
}

// token returns a usable token. stale is empty for an ordinary lookup, or the
// rejected token when refreshing.
func (m *ZentaoTokenManager) token(ctx context.Context, creds ZentaoCredentials, stale string) (string, error) {
	hash := sha256.Sum256([]byte(creds.Password))
	key := m.cacheKey(creds.Account)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Serve the cache when it holds a token this caller has not already seen
	// fail. On a refresh that means somebody else won the race.
	if entry, ok := m.tokens[key]; ok && entry.passwordHash == hash && entry.token != stale {
		return entry.token, nil
	}

	if f, ok := m.failures[key]; ok && m.now().Before(f.until) {
		m.logger.WarnContext(ctx, "skipping zentao login, still backing off",
			"account", creds.Account,
			"retry_in", f.until.Sub(m.now()).Round(time.Second).String(),
		)

		return "", f.err
	}

	token, err := m.login(ctx, creds)
	if err != nil {
		delete(m.tokens, key)
		m.recordFailure(ctx, key, creds.Account, err)

		return "", err
	}

	delete(m.failures, key)

	m.tokens[key] = tokenEntry{token: token, passwordHash: hash}

	return token, nil
}

// recordFailure holds off further logins for this account, doubling the wait
// on each consecutive rejection.
func (m *ZentaoTokenManager) recordFailure(ctx context.Context, key, account string, err error) {
	prev := m.failures[key]

	wait := loginBackoffBase << min(prev.count, 6)
	if wait > loginBackoffMax {
		wait = loginBackoffMax
	}

	m.failures[key] = failureEntry{err: err, until: m.now().Add(wait), count: prev.count + 1}

	m.logger.WarnContext(ctx, "zentao login failed, backing off",
		"account", account,
		"consecutive_failures", prev.count+1,
		"retry_in", wait.String(),
	)
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

		return "", &ZentaoLoginError{StatusCode: resp.StatusCode, Body: respBody}
	}

	var data struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}

	if err := json.Unmarshal(respBody, &data); err != nil {
		return "", fmt.Errorf("parse login response: %w", err)
	}

	if data.Token == "" {
		return "", fmt.Errorf("login response missing token")
	}

	m.logger.InfoContext(ctx, "zentao login succeeded", "account", creds.Account, "endpoint", endpoint)

	return data.Token, nil
}
