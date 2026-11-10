package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"host-panel-api/internal/model"
)

func TestChain_OrderOfExecution(t *testing.T) {
	var order []string

	mw := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+"-before")
				next.ServeHTTP(w, r)
				order = append(order, name+"-after")
			})
		}
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})

	chain := Chain(handler, mw("first"), mw("second"))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	chain.ServeHTTP(w, r)

	expected := []string{"first-before", "second-before", "handler", "second-after", "first-after"}
	if len(order) != len(expected) {
		t.Fatalf("execution order len = %d, want %d", len(order), len(expected))
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %q, want %q", i, order[i], v)
		}
	}
}

func TestChain_EmptyMiddlewares(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	chain := Chain(handler)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	chain.ServeHTTP(w, r)

	if !called {
		t.Error("handler should be called with empty middleware chain")
	}
}

func TestClaimsFromCtx(t *testing.T) {
	claims := map[string]any{"sub": "user123", "email": "user@test.com"}
	ctx := WithClaims(context.Background(), claims)

	got, ok := ClaimsFromCtx(ctx)
	if !ok {
		t.Fatal("ClaimsFromCtx returned false")
	}
	if got["sub"] != "user123" {
		t.Errorf("sub = %v, want %q", got["sub"], "user123")
	}
}

func TestClaimsFromCtx_NoClaims(t *testing.T) {
	_, ok := ClaimsFromCtx(context.Background())
	if ok {
		t.Error("ClaimsFromCtx should return false for empty context")
	}
}

func TestUserFromCtx(t *testing.T) {
	user := &model.User{Email: "test@test.com", Profile: model.ProfileAdmin}
	ctx := WithUser(context.Background(), user)

	got, ok := UserFromCtx(ctx)
	if !ok {
		t.Fatal("UserFromCtx returned false")
	}
	if got.Email != "test@test.com" {
		t.Errorf("Email = %q, want %q", got.Email, "test@test.com")
	}
}

func TestUserFromCtx_NoUser(t *testing.T) {
	_, ok := UserFromCtx(context.Background())
	if ok {
		t.Error("UserFromCtx should return false for empty context")
	}
}

func TestAPIKeyFromCtx(t *testing.T) {
	key := &model.APIKey{ID: "key1", Scopes: []string{"containers:read"}}
	ctx := WithAPIKey(context.Background(), key)

	got, ok := APIKeyFromCtx(ctx)
	if !ok {
		t.Fatal("APIKeyFromCtx returned false")
	}
	if got.ID != "key1" {
		t.Errorf("ID = %q, want %q", got.ID, "key1")
	}
}

func TestAPIKeyFromCtx_NoAPIKey(t *testing.T) {
	_, ok := APIKeyFromCtx(context.Background())
	if ok {
		t.Error("APIKeyFromCtx should return false for empty context")
	}
}

func TestRequireScope_NoAPIKey(t *testing.T) {
	// JWT-authenticated requests (no API key) always pass
	ctx := context.Background()
	if !RequireScope(ctx, "containers:actions") {
		t.Error("RequireScope should return true when no API key is present")
	}
}

func TestRequireScope_WithMatchingScope(t *testing.T) {
	key := &model.APIKey{ID: "k1", Scopes: []string{"containers:read", "containers:logs"}}
	ctx := WithAPIKey(context.Background(), key)

	if !RequireScope(ctx, "containers:read") {
		t.Error("RequireScope should return true for matching scope")
	}
}

func TestRequireScope_WithWildcard(t *testing.T) {
	key := &model.APIKey{ID: "k1", Scopes: []string{"*"}}
	ctx := WithAPIKey(context.Background(), key)

	if !RequireScope(ctx, "containers:actions") {
		t.Error("RequireScope should return true for wildcard scope")
	}
}

func TestRequireScope_WithoutMatchingScope(t *testing.T) {
	key := &model.APIKey{ID: "k1", Scopes: []string{"containers:read"}}
	ctx := WithAPIKey(context.Background(), key)

	if RequireScope(ctx, "containers:actions") {
		t.Error("RequireScope should return false for non-matching scope")
	}
}

func TestRequireScope_LegacyEmptyScopes(t *testing.T) {
	key := &model.APIKey{ID: "k1", ExpiresAt: time.Now().Add(time.Hour)}
	ctx := WithAPIKey(context.Background(), key)

	if !RequireScope(ctx, "containers:actions") {
		t.Error("RequireScope should return true for legacy keys with empty scopes")
	}
}
