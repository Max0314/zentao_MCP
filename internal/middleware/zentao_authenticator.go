package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// ZentaoAuthenticator verifies credentials before the MCP protocol handler,
// including initialization, tool discovery and index queries with zero hits.
type ZentaoAuthenticator struct {
	manager          *ZentaoTokenManager
	opts             ZentaoAccessOptions
	requests, logins *accessLimiter
}

func NewZentaoAuthenticator(manager *ZentaoTokenManager, opts ZentaoAccessOptions) *ZentaoAuthenticator {
	opts = opts.normalized()
	return &ZentaoAuthenticator{manager: manager, opts: opts,
		requests: newAccessLimiter(opts.RequestRate, opts.RequestBurst, opts.IPRequestRate, opts.IPRequestBurst),
		logins:   newAccessLimiter(opts.LoginRate, opts.LoginBurst, opts.IPLoginRate, opts.IPLoginBurst)}
}

func (a *ZentaoAuthenticator) Wrap(handler http.Handler) http.Handler {
	// Keep the generic extractor for non-ZenTao services. Only these routes
	// use the strict gate and translate legacy Bearer tokens to ZenTao's Token.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, a.opts.TrustedProxies)
		if err := a.requests.allow(ip); err != nil {
			writeZentaoAuthError(w, err)
			return
		}
		account, password := r.Header.Get("zentao-account"), r.Header.Get("zentao-password")
		if (account != "") != (password != "") || (account == "" && password == "" && r.Header.Get("token") == "" && r.Header.Get("Authorization") == "") {
			writeZentaoAuthError(w, &ZentaoLoginError{StatusCode: http.StatusUnauthorized})
			return
		}
		NewAuthorizationHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if creds, ok := GetZentaoCredentials(r.Context()); ok {
				creds.Account = strings.TrimSpace(creds.Account)
				if creds.Account == "" || len(creds.Account) > 128 || len(creds.Password) > 4096 {
					writeZentaoAuthError(w, &ZentaoLoginError{StatusCode: http.StatusUnauthorized})
					return
				}
				if _, err := a.manager.TokenWithLoginGuard(r.Context(), creds, func() error { return a.logins.allow(ip) }); err != nil {
					writeZentaoAuthError(w, err)
					return
				}
				r = r.WithContext(WithZentaoCredentials(r.Context(), creds.Account, creds.Password))
			} else {
				token := GetAuthorization(r.Context())
				if token == "" || len(token) > 4096 {
					writeZentaoAuthError(w, &ZentaoLoginError{StatusCode: http.StatusUnauthorized})
					return
				}
				if err := a.validateToken(r.Context(), token); err != nil {
					writeZentaoAuthError(w, err)
					return
				}
				r = r.WithContext(WithAuthorizationSource(r.Context(), true))
			}
			handler.ServeHTTP(w, r)
		})).ServeHTTP(w, r)
	})
}

func (a *ZentaoAuthenticator) validateToken(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	u, err := url.Parse(a.manager.baseURL)
	if err != nil {
		return err
	}
	u.Path = path.Join(u.Path, "user")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Token", token)
	resp, err := a.manager.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &ZentaoLoginError{StatusCode: resp.StatusCode}
	}
	var data struct {
		Profile struct {
			Account string `json:"account"`
		} `json:"profile"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&data); err != nil {
		return err
	}
	if data.Profile.Account == "" {
		return &ZentaoLoginError{StatusCode: http.StatusUnauthorized}
	}
	return nil
}

// Do not reflect upstream bodies: login failures can contain account details,
// debug output or secrets. Distinguish bad credentials from an unavailable API.
func writeZentaoAuthError(w http.ResponseWriter, err error) {
	status, message := http.StatusBadGateway, "禅道认证服务暂时不可用，请稍后重试"
	var loginErr *ZentaoLoginError
	if errors.As(err, &loginErr) {
		switch loginErr.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
			status, message = http.StatusUnauthorized, "禅道认证失败，请检查账号、密码或 token"
		case http.StatusTooManyRequests:
			status, message = http.StatusTooManyRequests, "登录或请求触发保护，请稍后重试"
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(AuthFailureHeader, "1")
	body := map[string]any{"error": message}
	if loginErr != nil && loginErr.RetryAfter > 0 {
		seconds := max(1, int((loginErr.RetryAfter+time.Second-1)/time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		body["retryAfterSeconds"] = seconds
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
