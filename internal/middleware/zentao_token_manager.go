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
)

const maxLoginResponseSize = 1 << 20

type tokenEntry struct {
	token        string
	passwordHash [32]byte
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

	mu     sync.Mutex
	tokens map[string]tokenEntry
}

// NewZentaoTokenManager creates a token manager for one upstream Zentao base URL.
func NewZentaoTokenManager(baseURL string, client *http.Client) *ZentaoTokenManager {
	if client == nil {
		client = http.DefaultClient
	}

	return &ZentaoTokenManager{
		baseURL: baseURL,
		client:  client,
		logger:  slog.Default().With("component", "middleware.zentao_auth"),
		tokens:  make(map[string]tokenEntry),
	}
}

// Token returns a cached token for the account or logs in when no matching token exists.
func (m *ZentaoTokenManager) Token(ctx context.Context, creds ZentaoCredentials) (string, error) {
	return m.token(ctx, creds, false)
}

// Refresh clears the cached token for the account and logs in again.
func (m *ZentaoTokenManager) Refresh(ctx context.Context, creds ZentaoCredentials) (string, error) {
	return m.token(ctx, creds, true)
}

func (m *ZentaoTokenManager) token(ctx context.Context, creds ZentaoCredentials, force bool) (string, error) {
	hash := sha256.Sum256([]byte(creds.Password))
	key := m.cacheKey(creds.Account)

	m.mu.Lock()
	defer m.mu.Unlock()

	if !force {
		if entry, ok := m.tokens[key]; ok && entry.passwordHash == hash {
			return entry.token, nil
		}
	}

	token, err := m.login(ctx, creds)
	if err != nil {
		delete(m.tokens, key)
		return "", err
	}

	m.tokens[key] = tokenEntry{token: token, passwordHash: hash}

	return token, nil
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
