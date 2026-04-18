package auth

import (
	"context"

	"agent-gateway/internal/store"
)

type Principal struct {
	User   store.User
	APIKey store.APIKey
}

type contextKey string

const principalKey contextKey = "principal"

func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalKey, principal)
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	principal, ok := ctx.Value(principalKey).(*Principal)
	return principal, ok
}
