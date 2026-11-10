package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"host-panel-api/internal/model"
)

func newTestUsersHandler() *UsersHandler {
	return &UsersHandler{
		Store:       newMockUserStore(),
		APIKeyStore: newMockAPIKeyStore(),
		Logger:      testLogger(),
		Audit:       testLogger(),
	}
}

func TestUsersHandler_Me_Success(t *testing.T) {
	h := newTestUsersHandler()

	r := httptest.NewRequest(http.MethodGet, "/api/users/me", nil)
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Me(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp userResponse
	json.NewDecoder(w.Result().Body).Decode(&resp)

	if resp.Email != "admin@test.com" {
		t.Errorf("Email = %q, want %q", resp.Email, "admin@test.com")
	}
	if resp.Profile != model.ProfileAdmin {
		t.Errorf("Profile = %q, want %q", resp.Profile, model.ProfileAdmin)
	}
}

func TestUsersHandler_Me_NoAuth(t *testing.T) {
	h := newTestUsersHandler()

	r := httptest.NewRequest(http.MethodGet, "/api/users/me", nil)
	w := httptest.NewRecorder()

	h.Me(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestUsersHandler_List_Success(t *testing.T) {
	h := newTestUsersHandler()
	store := h.Store.(*mockUserStore)
	store.Set("alice@test.com", &model.User{Email: "alice@test.com", Name: "Alice", Profile: model.ProfileAdmin})
	store.Set("bob@test.com", &model.User{Email: "bob@test.com", Name: "Bob", Profile: model.ProfileRestricted})

	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.List(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp []userResponse
	json.NewDecoder(w.Result().Body).Decode(&resp)

	if len(resp) != 2 {
		t.Errorf("len = %d, want 2", len(resp))
	}
}

func TestUsersHandler_Switch_AdminToRestricted(t *testing.T) {
	h := newTestUsersHandler()
	store := h.Store.(*mockUserStore)
	store.Set("bob@test.com", &model.User{Email: "bob@test.com", Name: "Bob", Profile: model.ProfileAdmin})

	r := httptest.NewRequest(http.MethodPost, "/api/users/bob@test.com/switch", nil)
	r.SetPathValue("email", "bob@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Switch(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	u, _ := store.Get("bob@test.com")
	if u.Profile != model.ProfileRestricted {
		t.Errorf("Profile = %q, want %q", u.Profile, model.ProfileRestricted)
	}
}

func TestUsersHandler_Switch_RestrictedToAdmin(t *testing.T) {
	h := newTestUsersHandler()
	store := h.Store.(*mockUserStore)
	store.Set("bob@test.com", &model.User{Email: "bob@test.com", Name: "Bob", Profile: model.ProfileRestricted})

	r := httptest.NewRequest(http.MethodPost, "/api/users/bob@test.com/switch", nil)
	r.SetPathValue("email", "bob@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Switch(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	u, _ := store.Get("bob@test.com")
	if u.Profile != model.ProfileAdmin {
		t.Errorf("Profile = %q, want %q", u.Profile, model.ProfileAdmin)
	}
}

func TestUsersHandler_Switch_NotFound(t *testing.T) {
	h := newTestUsersHandler()

	r := httptest.NewRequest(http.MethodPost, "/api/users/nobody@test.com/switch", nil)
	r.SetPathValue("email", "nobody@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Switch(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUsersHandler_Restrict_Success(t *testing.T) {
	h := newTestUsersHandler()
	store := h.Store.(*mockUserStore)
	store.Set("bob@test.com", &model.User{
		Email:       "bob@test.com",
		Profile:     model.ProfileAdmin,
		Permissions: []string{"ns1", "ns2"},
	})

	r := httptest.NewRequest(http.MethodPost, "/api/users/bob@test.com/restrict", nil)
	r.SetPathValue("email", "bob@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Restrict(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	u, _ := store.Get("bob@test.com")
	if u.Profile != model.ProfileRestricted {
		t.Errorf("Profile = %q, want %q", u.Profile, model.ProfileRestricted)
	}
	if len(u.Permissions) != 0 {
		t.Errorf("Permissions should be cleared, got %v", u.Permissions)
	}
}

func TestUsersHandler_Restrict_NotFound(t *testing.T) {
	h := newTestUsersHandler()

	r := httptest.NewRequest(http.MethodPost, "/api/users/nobody@test.com/restrict", nil)
	r.SetPathValue("email", "nobody@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Restrict(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUsersHandler_UpdateACL_Success(t *testing.T) {
	h := newTestUsersHandler()
	store := h.Store.(*mockUserStore)
	store.Set("bob@test.com", &model.User{Email: "bob@test.com", Profile: model.ProfileRestricted})

	body := `{"permissions":["ns1","ns2"]}`
	r := httptest.NewRequest(http.MethodPut, "/api/users/bob@test.com/acl", bytes.NewBufferString(body))
	r.SetPathValue("email", "bob@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.UpdateACL(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	u, _ := store.Get("bob@test.com")
	if len(u.Permissions) != 2 {
		t.Errorf("Permissions len = %d, want 2", len(u.Permissions))
	}
}

func TestUsersHandler_UpdateACL_InvalidBody(t *testing.T) {
	h := newTestUsersHandler()
	store := h.Store.(*mockUserStore)
	store.Set("bob@test.com", &model.User{Email: "bob@test.com"})

	r := httptest.NewRequest(http.MethodPut, "/api/users/bob@test.com/acl", bytes.NewBufferString("{invalid"))
	r.SetPathValue("email", "bob@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.UpdateACL(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUsersHandler_UpdateACL_NotFound(t *testing.T) {
	h := newTestUsersHandler()

	body := `{"permissions":["ns1"]}`
	r := httptest.NewRequest(http.MethodPut, "/api/users/nobody@test.com/acl", bytes.NewBufferString(body))
	r.SetPathValue("email", "nobody@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.UpdateACL(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUsersHandler_UpdateACL_NullPermissions(t *testing.T) {
	h := newTestUsersHandler()
	store := h.Store.(*mockUserStore)
	store.Set("bob@test.com", &model.User{Email: "bob@test.com", Permissions: []string{"old"}})

	body := `{}`
	r := httptest.NewRequest(http.MethodPut, "/api/users/bob@test.com/acl", bytes.NewBufferString(body))
	r.SetPathValue("email", "bob@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.UpdateACL(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	u, _ := store.Get("bob@test.com")
	if u.Permissions == nil {
		t.Error("Permissions should default to empty slice, not nil")
	}
}

func TestUsersHandler_Delete_Success(t *testing.T) {
	h := newTestUsersHandler()
	store := h.Store.(*mockUserStore)
	store.Set("bob@test.com", &model.User{Email: "bob@test.com"})

	r := httptest.NewRequest(http.MethodDelete, "/api/users/bob@test.com", nil)
	r.SetPathValue("email", "bob@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Delete(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	if _, ok := store.Get("bob@test.com"); ok {
		t.Error("user should have been deleted")
	}
}

func TestUsersHandler_Delete_CascadeAPIKeys(t *testing.T) {
	h := newTestUsersHandler()
	userStore := h.Store.(*mockUserStore)
	apiKeyStore := h.APIKeyStore.(*mockAPIKeyStore)

	userStore.Set("bob@test.com", &model.User{Email: "bob@test.com"})
	apiKeyStore.Create(&model.APIKey{
		ID:        "key1",
		KeyHash:   "h1",
		UserEmail: "bob@test.com",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	r := httptest.NewRequest(http.MethodDelete, "/api/users/bob@test.com", nil)
	r.SetPathValue("email", "bob@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Delete(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	keys := apiKeyStore.List("bob@test.com")
	if len(keys) != 0 {
		t.Errorf("API keys should have been cascade-deleted, got %d", len(keys))
	}
}

func TestUsersHandler_Delete_NotFound(t *testing.T) {
	h := newTestUsersHandler()

	r := httptest.NewRequest(http.MethodDelete, "/api/users/nobody@test.com", nil)
	r.SetPathValue("email", "nobody@test.com")
	r = adminCtx(r)
	w := httptest.NewRecorder()

	h.Delete(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUsersHandler_Delete_NoAuth(t *testing.T) {
	h := newTestUsersHandler()

	r := httptest.NewRequest(http.MethodDelete, "/api/users/bob@test.com", nil)
	r.SetPathValue("email", "bob@test.com")
	w := httptest.NewRecorder()

	h.Delete(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}
