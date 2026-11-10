package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"host-panel-api/internal/model"
)

// mockAPIKeyStore implements store.APIKeyStore for middleware testing.
type mockAPIKeyStore struct {
	mu   sync.RWMutex
	keys map[string]*model.APIKey // by hash
	byID map[string]*model.APIKey
}

func newMockAPIKeyStore() *mockAPIKeyStore {
	return &mockAPIKeyStore{
		keys: make(map[string]*model.APIKey),
		byID: make(map[string]*model.APIKey),
	}
}

func (s *mockAPIKeyStore) Create(key *model.APIKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key.KeyHash] = key
	s.byID[key.ID] = key
}

func (s *mockAPIKeyStore) List(email string) []*model.APIKey { return nil }

func (s *mockAPIKeyStore) GetByHash(hash string) (*model.APIKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.keys[hash]
	return k, ok
}

func (s *mockAPIKeyStore) Delete(id, email string) bool { return false }
func (s *mockAPIKeyStore) DeleteByEmail(email string)   {}
func (s *mockAPIKeyStore) UpdateLastUsed(id string)     {}

func apikeyTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func hashKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func TestAPIKeyOrJWT_NoHeader_FallsBackToJWT(t *testing.T) {
	apiKeyStore := newMockAPIKeyStore()
	userStore := newMockStore()

	fallbackCalled := false
	mainCalled := false

	main := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mainCalled = true
	})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalled = true
	})

	handler := APIKeyOrJWT(apiKeyStore, userStore, apikeyTestLogger(), main, fallback)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !fallbackCalled {
		t.Error("fallback should be called when no X-API-Key header")
	}
	if mainCalled {
		t.Error("main handler should not be called")
	}
}

func TestAPIKeyOrJWT_ValidKey_InjectsUser(t *testing.T) {
	apiKeyStore := newMockAPIKeyStore()
	userStore := newMockStore()

	rawKey := "loch_test-api-key-value"
	hash := hashKey(rawKey)

	apiKeyStore.Create(&model.APIKey{
		ID:        "key1",
		KeyHash:   hash,
		UserEmail: "alice@test.com",
		Scopes:    []string{"containers:read"},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	userStore.users["alice@test.com"] = &model.User{
		Email:   "alice@test.com",
		Name:    "Alice",
		Profile: model.ProfileAdmin,
	}

	var gotUser *model.User
	var gotAPIKey *model.APIKey

	main := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, _ = UserFromCtx(r.Context())
		gotAPIKey, _ = APIKeyFromCtx(r.Context())
	})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("fallback should not be called with valid API key")
	})

	handler := APIKeyOrJWT(apiKeyStore, userStore, apikeyTestLogger(), main, fallback)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotUser == nil {
		t.Fatal("user should be injected into context")
	}
	if gotUser.Email != "alice@test.com" {
		t.Errorf("Email = %q, want %q", gotUser.Email, "alice@test.com")
	}
	if gotAPIKey == nil {
		t.Fatal("API key should be injected into context")
	}
	if gotAPIKey.ID != "key1" {
		t.Errorf("APIKey ID = %q, want %q", gotAPIKey.ID, "key1")
	}
}

func TestAPIKeyOrJWT_InvalidKey_Returns401(t *testing.T) {
	apiKeyStore := newMockAPIKeyStore()
	userStore := newMockStore()

	main := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("main should not be called")
	})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("fallback should not be called")
	})

	handler := APIKeyOrJWT(apiKeyStore, userStore, apikeyTestLogger(), main, fallback)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", "invalid-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAPIKeyOrJWT_ExpiredKey_Returns401(t *testing.T) {
	apiKeyStore := newMockAPIKeyStore()
	userStore := newMockStore()

	rawKey := "loch_expired-key"
	hash := hashKey(rawKey)

	apiKeyStore.Create(&model.APIKey{
		ID:        "key1",
		KeyHash:   hash,
		UserEmail: "alice@test.com",
		ExpiresAt: time.Now().Add(-time.Hour), // expired
	})

	main := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("main should not be called")
	})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("fallback should not be called")
	})

	handler := APIKeyOrJWT(apiKeyStore, userStore, apikeyTestLogger(), main, fallback)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAPIKeyOrJWT_UserNotFound_Returns401(t *testing.T) {
	apiKeyStore := newMockAPIKeyStore()
	userStore := newMockStore() // empty user store

	rawKey := "loch_orphaned-key"
	hash := hashKey(rawKey)

	apiKeyStore.Create(&model.APIKey{
		ID:        "key1",
		KeyHash:   hash,
		UserEmail: "deleted@test.com",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	main := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("main should not be called")
	})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("fallback should not be called")
	})

	handler := APIKeyOrJWT(apiKeyStore, userStore, apikeyTestLogger(), main, fallback)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}
