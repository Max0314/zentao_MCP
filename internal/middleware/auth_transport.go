package middleware

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type authTransport struct {
	base         http.RoundTripper
	tokenManager *ZentaoTokenManager
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if creds, ok := GetZentaoCredentials(req.Context()); ok {
		return t.roundTripWithManagedToken(req, creds)
	}

	token := GetAuthorization(req.Context())
	if token == "" {
		return t.base.RoundTrip(req)
	}

	return t.roundTripWithToken(req, token, IsFromTokenHeader(req.Context()))
}

func (t *authTransport) roundTripWithManagedToken(req *http.Request, creds ZentaoCredentials) (*http.Response, error) {
	token, err := t.tokenManager.Token(req.Context(), creds)
	if err != nil {
		return loginErrorResponse(req, err)
	}

	resp, err := t.roundTripWithToken(req, token, true)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || !canReplay(req) {
		return resp, err
	}

	_ = resp.Body.Close()

	token, err = t.tokenManager.Refresh(req.Context(), creds)
	if err != nil {
		return loginErrorResponse(req, err)
	}

	return t.roundTripWithToken(req, token, true)
}

func (t *authTransport) roundTripWithToken(req *http.Request, token string, fromTokenHeader bool) (*http.Response, error) {
	r, err := cloneRequestForAttempt(req)
	if err != nil {
		return nil, err
	}

	if fromTokenHeader {
		r.Header.Set("token", token)
	} else {
		r.Header.Set("Authorization", "Bearer "+token)
	}

	return t.base.RoundTrip(r)
}

func cloneRequestForAttempt(req *http.Request) (*http.Request, error) {
	r := req.Clone(req.Context())

	if req.Body == nil {
		return r, nil
	}

	if req.GetBody == nil {
		r.Body = req.Body
		return r, nil
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}

	r.Body = body

	return r, nil
}

// canReplay reports whether the request can be sent a second time after a 401.
// A request with no body is always replayable, including when the caller
// expressed that as http.NoBody, which NewRequest stores as a non-nil Body
// with no GetBody.
func canReplay(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

func loginErrorResponse(req *http.Request, err error) (*http.Response, error) {
	var loginErr *ZentaoLoginError
	if !errors.As(err, &loginErr) {
		return nil, err
	}

	body := loginErr.Body
	if len(body) == 0 {
		body = []byte(`{"error":"Unauthorized"}`)
	}

	header := make(http.Header)
	header.Set("Content-Type", "application/json")

	return &http.Response{
		StatusCode: loginErr.StatusCode,
		Status:     fmt.Sprintf("%d %s", loginErr.StatusCode, http.StatusText(loginErr.StatusCode)),
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

// WithZentaoAuth wraps an HTTP client to forward legacy tokens or manage Zentao user tokens.
func WithZentaoAuth(c *http.Client, baseURL string) *http.Client {
	if c == nil {
		c = http.DefaultClient
	}

	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}

	loginClient := &http.Client{
		Transport:     base,
		CheckRedirect: c.CheckRedirect,
		Jar:           c.Jar,
		Timeout:       c.Timeout,
	}

	return &http.Client{
		Transport: &authTransport{
			base:         base,
			tokenManager: NewZentaoTokenManager(baseURL, loginClient),
		},
		CheckRedirect: c.CheckRedirect,
		Jar:           c.Jar,
		Timeout:       c.Timeout,
	}
}
