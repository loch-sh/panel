package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"host-panel-api/internal/model"
)

func TestAdmin_AdminAllowed(t *testing.T) {
	called := false
	handler := Admin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithUser(req.Context(), &model.User{
		Email:   "admin@example.com",
		Profile: model.ProfileAdmin,
	})
	req = req.WithContext(ctx)

	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler must be called for an admin")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusOK)
	}
}

func TestAdmin_RestrictedDenied(t *testing.T) {
	called := false
	handler := Admin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithUser(req.Context(), &model.User{
		Email:   "user@example.com",
		Profile: model.ProfileRestricted,
	})
	req = req.WithContext(ctx)

	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler must NOT be called for a restricted user")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusForbidden)
	}
}

func TestAdmin_MissingUser(t *testing.T) {
	called := false
	handler := Admin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No user in context

	handler.ServeHTTP(rr, req)

	if called {
		t.Error("next handler must NOT be called without user in context")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusForbidden)
	}
}
