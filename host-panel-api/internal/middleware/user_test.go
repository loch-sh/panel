package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"host-panel-api/internal/model"
)

// mockUserStore implements store.UserStore in memory for tests.
type mockUserStore struct {
	mu    sync.RWMutex
	users map[string]*model.User
}

func newMockStore() *mockUserStore {
	return &mockUserStore{users: make(map[string]*model.User)}
}

func (s *mockUserStore) Get(email string) (*model.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[email]
	return u, ok
}

func (s *mockUserStore) Set(email string, user *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[email] = user
}

func (s *mockUserStore) Delete(email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.users[email]
	delete(s.users, email)
	return ok
}

func (s *mockUserStore) List() []*model.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*model.User, 0, len(s.users))
	for _, u := range s.users {
		list = append(list, u)
	}
	return list
}

func (s *mockUserStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

func (s *mockUserStore) GetOrCreate(email string, create func(currentCount int) *model.User) (*model.User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[email]; ok {
		return u, true
	}
	u := create(len(s.users))
	s.users[email] = u
	return u, false
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestUser_ExistingUser(t *testing.T) {
	store := newMockStore()
	existing := &model.User{
		Email:   "alice@example.com",
		Name:    "Alice",
		Profile: model.ProfileAdmin,
	}
	store.Set("alice@example.com", existing)

	var gotUser *model.User
	handler := User(store, testLogger(), testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromCtx(r.Context())
		if ok {
			gotUser = u
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithClaims(req.Context(), map[string]any{"email": "alice@example.com"})
	req = req.WithContext(ctx)

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200", rr.Code)
	}
	if gotUser == nil || gotUser.Email != "alice@example.com" {
		t.Error("existing user must be loaded into context")
	}
	if gotUser.Profile != model.ProfileAdmin {
		t.Errorf("profile = %q, expected %q", gotUser.Profile, model.ProfileAdmin)
	}
}

func TestUser_FirstUserBecomesAdmin(t *testing.T) {
	store := newMockStore() // empty store

	var gotUser *model.User
	handler := User(store, testLogger(), testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFromCtx(r.Context())
		gotUser = u
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithClaims(req.Context(), map[string]any{
		"email": "first@example.com",
		"name":  "Premier Utilisateur",
	})
	req = req.WithContext(ctx)

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200", rr.Code)
	}
	if gotUser == nil {
		t.Fatal("user must be created")
	}
	if gotUser.Profile != model.ProfileAdmin {
		t.Errorf("first user must be admin, profile = %q", gotUser.Profile)
	}
}

func TestUser_SubsequentUserBecomesRestricted(t *testing.T) {
	store := newMockStore()
	store.Set("admin@example.com", &model.User{
		Email:   "admin@example.com",
		Profile: model.ProfileAdmin,
	})

	var gotUser *model.User
	handler := User(store, testLogger(), testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFromCtx(r.Context())
		gotUser = u
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithClaims(req.Context(), map[string]any{
		"email": "newuser@example.com",
		"name":  "Nouvel Utilisateur",
	})
	req = req.WithContext(ctx)

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200", rr.Code)
	}
	if gotUser == nil {
		t.Fatal("user must be created")
	}
	if gotUser.Profile != model.ProfileRestricted {
		t.Errorf("subsequent users must be restricted, profile = %q", gotUser.Profile)
	}
}

func TestUser_MissingClaims(t *testing.T) {
	store := newMockStore()
	called := false

	handler := User(store, testLogger(), testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No claims in context

	handler.ServeHTTP(rr, req)

	if called {
		t.Error("handler must not be called without JWT claims")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestUser_MissingEmail(t *testing.T) {
	store := newMockStore()
	called := false

	handler := User(store, testLogger(), testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithClaims(req.Context(), map[string]any{
		"name": "Sans Email",
		// neither "email" nor "preferred_username"
	})
	req = req.WithContext(ctx)

	handler.ServeHTTP(rr, req)

	if called {
		t.Error("handler must not be called without email")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusForbidden)
	}
}

func TestUser_FallbackToPreferredUsername(t *testing.T) {
	store := newMockStore()

	var gotUser *model.User
	handler := User(store, testLogger(), testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFromCtx(r.Context())
		gotUser = u
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithClaims(req.Context(), map[string]any{
		"preferred_username": "bob@example.com",
		"name":               "Bob",
	})
	req = req.WithContext(ctx)

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200", rr.Code)
	}
	if gotUser == nil || gotUser.Email != "bob@example.com" {
		t.Error("preferred_username must be used as fallback for email")
	}
}
