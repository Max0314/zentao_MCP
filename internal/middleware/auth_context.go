// Package middleware provides HTTP middleware for authorization.
package middleware

import (
	"context"
	"strings"
)

type authorizationContextKey struct{}
type authorizationSourceKey struct{}
type zentaoCredentialsContextKey struct{}

// ZentaoCredentials stores the user credentials used to obtain a Zentao token.
type ZentaoCredentials struct {
	Account  string
	Password string
}

// WithAuthorization stores the bearer token in the context.
func WithAuthorization(ctx context.Context, token string) context.Context {
	if v, ok := strings.CutPrefix(token, "Bearer "); ok {
		token = v
	}

	return context.WithValue(ctx, authorizationContextKey{}, token)
}

// GetAuthorization retrieves the bearer token from the context.
func GetAuthorization(ctx context.Context) string {
	token, ok := ctx.Value(authorizationContextKey{}).(string)
	if !ok {
		return ""
	}

	return token
}

// WithAuthorizationSource marks whether the token comes from the custom `token` header.
func WithAuthorizationSource(ctx context.Context, fromTokenHeader bool) context.Context {
	return context.WithValue(ctx, authorizationSourceKey{}, fromTokenHeader)
}

// IsFromTokenHeader reports whether the auth value was provided via the `token` header.
func IsFromTokenHeader(ctx context.Context) bool {
	v, ok := ctx.Value(authorizationSourceKey{}).(bool)
	if !ok {
		return false
	}

	return v
}

// WithZentaoCredentials stores Zentao account credentials in the context.
func WithZentaoCredentials(ctx context.Context, account, password string) context.Context {
	return context.WithValue(ctx, zentaoCredentialsContextKey{}, ZentaoCredentials{
		Account:  account,
		Password: password,
	})
}

// GetZentaoCredentials retrieves Zentao account credentials from the context.
func GetZentaoCredentials(ctx context.Context) (ZentaoCredentials, bool) {
	creds, ok := ctx.Value(zentaoCredentialsContextKey{}).(ZentaoCredentials)
	if !ok || creds.Account == "" || creds.Password == "" {
		return ZentaoCredentials{}, false
	}

	return creds, true
}
