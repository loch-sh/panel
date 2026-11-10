package store

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"host-panel-api/internal/model"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestUserStore(t *testing.T) *JSONFileStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.json")
	s, err := NewJSONFileStore(path, testLogger())
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	return s
}

func TestJSONFileStore_NewEmpty(t *testing.T) {
	s := newTestUserStore(t)
	if s.Count() != 0 {
		t.Errorf("Count() = %d, want 0", s.Count())
	}
	if users := s.List(); len(users) != 0 {
		t.Errorf("List() len = %d, want 0", len(users))
	}
}

func TestJSONFileStore_LoadExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")

	data := map[string]*model.User{
		"alice@test.com": {Email: "alice@test.com", Name: "Alice", Profile: model.ProfileAdmin},
	}
	raw, _ := json.MarshalIndent(data, "", "  ")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewJSONFileStore(path, testLogger())
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	if s.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", s.Count())
	}
	u, ok := s.Get("alice@test.com")
	if !ok {
		t.Fatal("Get(alice) returned false")
	}
	if u.Name != "Alice" {
		t.Errorf("Name = %q, want %q", u.Name, "Alice")
	}
}

func TestJSONFileStore_LoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewJSONFileStore(path, testLogger())
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	if s.Count() != 0 {
		t.Errorf("Count() = %d, want 0", s.Count())
	}
}

func TestJSONFileStore_LoadCorrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	if err := os.WriteFile(path, []byte("{invalid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewJSONFileStore(path, testLogger())
	if err == nil {
		t.Fatal("expected error for corrupted JSON")
	}
}

func TestJSONFileStore_GetSetDelete(t *testing.T) {
	s := newTestUserStore(t)

	user := &model.User{Email: "bob@test.com", Name: "Bob", Profile: model.ProfileRestricted}
	s.Set("bob@test.com", user)

	got, ok := s.Get("bob@test.com")
	if !ok {
		t.Fatal("Get returned false after Set")
	}
	if got.Name != "Bob" {
		t.Errorf("Name = %q, want %q", got.Name, "Bob")
	}

	if s.Count() != 1 {
		t.Errorf("Count() = %d, want 1", s.Count())
	}

	if !s.Delete("bob@test.com") {
		t.Error("Delete returned false for existing user")
	}
	if s.Delete("bob@test.com") {
		t.Error("Delete returned true for already-deleted user")
	}
	if s.Count() != 0 {
		t.Errorf("Count() = %d after delete, want 0", s.Count())
	}
}

func TestJSONFileStore_GetNotFound(t *testing.T) {
	s := newTestUserStore(t)
	_, ok := s.Get("nobody@test.com")
	if ok {
		t.Error("Get returned true for non-existent user")
	}
}

func TestJSONFileStore_List(t *testing.T) {
	s := newTestUserStore(t)
	s.Set("a@test.com", &model.User{Email: "a@test.com", Name: "A"})
	s.Set("b@test.com", &model.User{Email: "b@test.com", Name: "B"})
	s.Set("c@test.com", &model.User{Email: "c@test.com", Name: "C"})

	users := s.List()
	if len(users) != 3 {
		t.Fatalf("List() len = %d, want 3", len(users))
	}
}

func TestJSONFileStore_GetOrCreate_Existing(t *testing.T) {
	s := newTestUserStore(t)
	s.Set("alice@test.com", &model.User{Email: "alice@test.com", Name: "Alice", Profile: model.ProfileAdmin})

	u, existed := s.GetOrCreate("alice@test.com", func(count int) *model.User {
		t.Fatal("create should not be called for existing user")
		return nil
	})
	if !existed {
		t.Error("expected existed=true")
	}
	if u.Name != "Alice" {
		t.Errorf("Name = %q, want %q", u.Name, "Alice")
	}
}

func TestJSONFileStore_GetOrCreate_FirstUserAdmin(t *testing.T) {
	s := newTestUserStore(t)

	u, existed := s.GetOrCreate("first@test.com", func(count int) *model.User {
		profile := model.ProfileRestricted
		if count == 0 {
			profile = model.ProfileAdmin
		}
		return &model.User{Email: "first@test.com", Name: "First", Profile: profile}
	})
	if existed {
		t.Error("expected existed=false for new user")
	}
	if u.Profile != model.ProfileAdmin {
		t.Errorf("Profile = %q, want %q (first user should be admin)", u.Profile, model.ProfileAdmin)
	}
}

func TestJSONFileStore_GetOrCreate_SubsequentUserRestricted(t *testing.T) {
	s := newTestUserStore(t)
	s.Set("existing@test.com", &model.User{Email: "existing@test.com"})

	u, existed := s.GetOrCreate("second@test.com", func(count int) *model.User {
		profile := model.ProfileRestricted
		if count == 0 {
			profile = model.ProfileAdmin
		}
		return &model.User{Email: "second@test.com", Name: "Second", Profile: profile}
	})
	if existed {
		t.Error("expected existed=false")
	}
	if u.Profile != model.ProfileRestricted {
		t.Errorf("Profile = %q, want %q", u.Profile, model.ProfileRestricted)
	}
}

func TestJSONFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")

	s1, err := NewJSONFileStore(path, testLogger())
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	s1.Set("persist@test.com", &model.User{Email: "persist@test.com", Name: "Persist", Profile: model.ProfileAdmin})

	// Reload from disk
	s2, err := NewJSONFileStore(path, testLogger())
	if err != nil {
		t.Fatalf("NewJSONFileStore reload: %v", err)
	}
	u, ok := s2.Get("persist@test.com")
	if !ok {
		t.Fatal("user not found after reload")
	}
	if u.Name != "Persist" {
		t.Errorf("Name = %q, want %q", u.Name, "Persist")
	}
}

func TestJSONFileStore_ConcurrentAccess(t *testing.T) {
	s := newTestUserStore(t)
	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			email := "user" + string(rune('A'+i)) + "@test.com"
			s.Set(email, &model.User{Email: email, Name: "User"})
			s.Get(email)
			s.List()
			s.Count()
		}(i)
	}

	wg.Wait()
	if s.Count() != 20 {
		t.Errorf("Count() = %d, want 20 after concurrent writes", s.Count())
	}
}

func TestJSONFileStore_DropsFunctionPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	data, _ := json.Marshal(map[string]*model.User{
		"u@x": {Email: "u@x", Profile: model.ProfileRestricted, Permissions: []string{"web", "fn-resize", "/fn-other"}},
	})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewJSONFileStore(path, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := s.Get("u@x")
	if len(u.Permissions) != 1 || u.Permissions[0] != "web" {
		t.Fatalf("permissions = %v", u.Permissions)
	}
}
