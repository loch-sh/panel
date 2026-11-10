package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"
)

func newTestAPIKeyHandler() *APIKeyHandler {
	return &APIKeyHandler{
		Store:  newMockAPIKeyStore(),
		Logger: testLogger(),
		Audit:  testLogger(),
	}
}

func TestAPIKeyHandler_Create_Success(t *testing.T) {
	h := newTestAPIKeyHandler()
	body := `{"name":"my-key","expiresInDays":30,"scopes":["containers:read"]}`

	r := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString(body))
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	var resp createAPIKeyResponse
	decodeJSON(t, w.Result().Body, &resp)

	if resp.Name != "my-key" {
		t.Errorf("Name = %q, want %q", resp.Name, "my-key")
	}
	if resp.Token == "" {
		t.Error("Token should not be empty")
	}
	if len(resp.Token) < len("loch_")+16 {
		t.Errorf("Token too short: %q", resp.Token)
	}
	if resp.ID == "" {
		t.Error("ID should not be empty")
	}
	if len(resp.Scopes) != 1 || resp.Scopes[0] != "containers:read" {
		t.Errorf("Scopes = %v, want [containers:read]", resp.Scopes)
	}
}

func TestAPIKeyHandler_Create_DefaultScopesWildcard(t *testing.T) {
	h := newTestAPIKeyHandler()
	body := `{"name":"legacy","expiresInDays":30}`

	r := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString(body))
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	var resp createAPIKeyResponse
	decodeJSON(t, w.Result().Body, &resp)

	if len(resp.Scopes) != 1 || resp.Scopes[0] != "*" {
		t.Errorf("Scopes = %v, want [*] (default wildcard)", resp.Scopes)
	}
}

func TestAPIKeyHandler_Create_EmptyName(t *testing.T) {
	h := newTestAPIKeyHandler()
	body := `{"name":"","expiresInDays":30,"scopes":["*"]}`

	r := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString(body))
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAPIKeyHandler_Create_NameTooLong(t *testing.T) {
	h := newTestAPIKeyHandler()
	longName := make([]byte, 101)
	for i := range longName {
		longName[i] = 'a'
	}
	body, _ := json.Marshal(map[string]any{"name": string(longName), "expiresInDays": 30, "scopes": []string{"*"}})

	r := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBuffer(body))
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAPIKeyHandler_Create_InvalidExpiry(t *testing.T) {
	tests := []struct {
		name string
		days int
	}{
		{"zero days", 0},
		{"negative days", -1},
		{"too many days", 366},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestAPIKeyHandler()
			body, _ := json.Marshal(map[string]any{"name": "test", "expiresInDays": tt.days, "scopes": []string{"*"}})

			r := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBuffer(body))
			r = adminCtx(r)
			w := httptest.NewRecorder()

			h.Create(w, r)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestAPIKeyHandler_Create_InvalidScopes(t *testing.T) {
	h := newTestAPIKeyHandler()
	body := `{"name":"test","expiresInDays":30,"scopes":["invalid_scope"]}`

	r := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString(body))
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAPIKeyHandler_Create_LimitReached(t *testing.T) {
	h := newTestAPIKeyHandler()
	store := h.Store.(*mockAPIKeyStore)

	// Pre-fill 20 keys
	for i := range 20 {
		store.Create(&model.APIKey{
			ID:        "id" + string(rune('A'+i)),
			KeyHash:   "hash" + string(rune('A'+i)),
			UserEmail: "admin@test.com",
			ExpiresAt: time.Now().Add(time.Hour),
		})
	}

	body := `{"name":"one-too-many","expiresInDays":30,"scopes":["*"]}`
	r := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString(body))
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestAPIKeyHandler_Create_NoAuth(t *testing.T) {
	h := newTestAPIKeyHandler()
	body := `{"name":"test","expiresInDays":30,"scopes":["*"]}`

	r := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString(body))
	// No user context
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestAPIKeyHandler_Create_InvalidJSON(t *testing.T) {
	h := newTestAPIKeyHandler()

	r := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString("{invalid"))
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAPIKeyHandler_List_Success(t *testing.T) {
	h := newTestAPIKeyHandler()
	store := h.Store.(*mockAPIKeyStore)
	store.Create(&model.APIKey{
		ID:        "id1",
		Name:      "key1",
		KeyHash:   "h1",
		KeyPrefix: "loch_abcd",
		UserEmail: "admin@test.com",
		Scopes:    []string{"*"},
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	})

	r := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.List(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp []listAPIKeyResponse
	decodeJSON(t, w.Result().Body, &resp)

	if len(resp) != 1 {
		t.Fatalf("len = %d, want 1", len(resp))
	}
	if resp[0].Name != "key1" {
		t.Errorf("Name = %q, want %q", resp[0].Name, "key1")
	}
}

func TestAPIKeyHandler_List_NoAuth(t *testing.T) {
	h := newTestAPIKeyHandler()

	r := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	w := httptest.NewRecorder()

	h.List(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestAPIKeyHandler_Delete_Success(t *testing.T) {
	h := newTestAPIKeyHandler()
	store := h.Store.(*mockAPIKeyStore)
	store.Create(&model.APIKey{
		ID:        "id1",
		KeyHash:   "h1",
		UserEmail: "admin@test.com",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	r := httptest.NewRequest(http.MethodDelete, "/api/keys/id1", nil)
	r.SetPathValue("id", "id1")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Delete(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestAPIKeyHandler_Delete_NotFound(t *testing.T) {
	h := newTestAPIKeyHandler()

	r := httptest.NewRequest(http.MethodDelete, "/api/keys/nonexistent", nil)
	r.SetPathValue("id", "nonexistent")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Delete(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestAPIKeyHandler_Delete_NoAuth(t *testing.T) {
	h := newTestAPIKeyHandler()

	r := httptest.NewRequest(http.MethodDelete, "/api/keys/id1", nil)
	r.SetPathValue("id", "id1")
	w := httptest.NewRecorder()

	h.Delete(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestAPIKeyHandler_Create_KeyCannotEscalate(t *testing.T) {
	h := newTestAPIKeyHandler()
	create := func(scopes string, caller *model.APIKey) int {
		body := `{"name":"k","expiresInDays":30,"scopes":` + scopes + `}`
		r := adminCtx(httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString(body)))
		if caller != nil {
			r = r.WithContext(middleware.WithAPIKey(r.Context(), caller))
		}
		w := httptest.NewRecorder()
		h.Create(w, r)
		return w.Code
	}
	limited := &model.APIKey{ID: "k1", Scopes: []string{"containers:read", "containers:logs"}}
	if code := create(`["*"]`, limited); code != http.StatusForbidden {
		t.Fatalf("a limited key must not create a full key: %d", code)
	}
	if code := create(`["containers:actions"]`, limited); code != http.StatusForbidden {
		t.Fatalf("a limited key must not create a key with another scope: %d", code)
	}
	if code := create(`["containers:read"]`, limited); code != http.StatusCreated {
		t.Fatalf("a subset of its scopes is allowed: %d", code)
	}
	if code := create(`["*"]`, &model.APIKey{ID: "k2", Scopes: []string{"*"}}); code != http.StatusCreated {
		t.Fatalf("a full key may create a full key: %d", code)
	}
	if code := create(`["*"]`, nil); code != http.StatusCreated {
		t.Fatalf("a browser session may create a full key: %d", code)
	}
}
