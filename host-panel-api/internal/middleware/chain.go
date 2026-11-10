package middleware

import (
	"context"
	"net/http"

	"host-panel-api/internal/model"
)

type contextKey int

const (
	ctxJWTClaims contextKey = iota
	ctxUser
	ctxAPIKey
)

// Chain composes middlewares in the given order.
// The first middleware in the list is the outermost.
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// ClaimsFromCtx extracts JWT claims from the context.
func ClaimsFromCtx(ctx context.Context) (map[string]any, bool) {
	claims, ok := ctx.Value(ctxJWTClaims).(map[string]any)
	return claims, ok
}

// WithClaims stores JWT claims in the context.
func WithClaims(ctx context.Context, claims map[string]any) context.Context {
	return context.WithValue(ctx, ctxJWTClaims, claims)
}

// UserFromCtx extracts the user from the context.
func UserFromCtx(ctx context.Context) (*model.User, bool) {
	u, ok := ctx.Value(ctxUser).(*model.User)
	return u, ok
}

// WithUser stores the user in the context.
//
// It also records the user for the audit trail (see AuditRequests): call it
// from the request goroutine only.
func WithUser(ctx context.Context, u *model.User) context.Context {
	if a := auditInfo(ctx); a != nil && u != nil {
		a.user = u.Email
	}
	return context.WithValue(ctx, ctxUser, u)
}

// APIKeyFromCtx extracts the API key from the context.
func APIKeyFromCtx(ctx context.Context) (*model.APIKey, bool) {
	k, ok := ctx.Value(ctxAPIKey).(*model.APIKey)
	return k, ok
}

// WithAPIKey stores the API key in the context.
//
// It also records the key id for the audit trail (see AuditRequests): call
// it from the request goroutine only.
func WithAPIKey(ctx context.Context, k *model.APIKey) context.Context {
	if a := auditInfo(ctx); a != nil && k != nil {
		a.apiKey = k.ID
	}
	return context.WithValue(ctx, ctxAPIKey, k)
}

// RequireScope checks if the request has the given scope.
// JWT-authenticated requests (no API key in context) always pass.
func RequireScope(ctx context.Context, scope string) bool {
	apiKey, ok := APIKeyFromCtx(ctx)
	if !ok {
		return true
	}
	return apiKey.HasScope(scope)
}
