package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"host-panel-api/internal/model"
)

func newTestAPIKeyStore(t *testing.T) *APIKeyFileStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "apikeys.json")
	s, err := NewAPIKeyFileStore(path, testLogger())
	if err != nil {
		t.Fatalf("NewAPIKeyFileStore: %v", err)
	}
	return s
}

func makeAPIKey(id, hash, email string) *model.APIKey {
	return &model.APIKey{
		ID:        id,
		Name:      "key-" + id,
		KeyHash:   hash,
		KeyPrefix: "loch_" + id,
		UserEmail: email,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}
}

func TestAPIKeyFileStore_CreateAndGetByHash(t *testing.T) {
	s := newTestAPIKeyStore(t)

	key := makeAPIKey("id1", "hash1", "alice@test.com")
	s.Create(key)

	got, ok := s.GetByHash("hash1")
	if !ok {
		t.Fatal("GetByHash returned false")
	}
	if got.ID != "id1" {
		t.Errorf("ID = %q, want %q", got.ID, "id1")
	}
	if got.UserEmail != "alice@test.com" {
		t.Errorf("UserEmail = %q, want %q", got.UserEmail, "alice@test.com")
	}
}

func TestAPIKeyFileStore_GetByHash_NotFound(t *testing.T) {
	s := newTestAPIKeyStore(t)
	_, ok := s.GetByHash("nonexistent")
	if ok {
		t.Error("GetByHash returned true for non-existent hash")
	}
}

func TestAPIKeyFileStore_List_FilterByEmail(t *testing.T) {
	s := newTestAPIKeyStore(t)

	s.Create(makeAPIKey("id1", "h1", "alice@test.com"))
	s.Create(makeAPIKey("id2", "h2", "alice@test.com"))
	s.Create(makeAPIKey("id3", "h3", "bob@test.com"))

	aliceKeys := s.List("alice@test.com")
	if len(aliceKeys) != 2 {
		t.Errorf("List(alice) len = %d, want 2", len(aliceKeys))
	}

	bobKeys := s.List("bob@test.com")
	if len(bobKeys) != 1 {
		t.Errorf("List(bob) len = %d, want 1", len(bobKeys))
	}

	noKeys := s.List("nobody@test.com")
	if len(noKeys) != 0 {
		t.Errorf("List(nobody) len = %d, want 0", len(noKeys))
	}
}

func TestAPIKeyFileStore_Delete_Success(t *testing.T) {
	s := newTestAPIKeyStore(t)
	s.Create(makeAPIKey("id1", "h1", "alice@test.com"))

	if !s.Delete("id1", "alice@test.com") {
		t.Error("Delete returned false for existing key")
	}

	_, ok := s.GetByHash("h1")
	if ok {
		t.Error("GetByHash should return false after delete")
	}
}

func TestAPIKeyFileStore_Delete_WrongEmail(t *testing.T) {
	s := newTestAPIKeyStore(t)
	s.Create(makeAPIKey("id1", "h1", "alice@test.com"))

	if s.Delete("id1", "bob@test.com") {
		t.Error("Delete should return false when email doesn't match")
	}

	// Key should still exist
	_, ok := s.GetByHash("h1")
	if !ok {
		t.Error("key should still exist after failed delete")
	}
}

func TestAPIKeyFileStore_Delete_NotFound(t *testing.T) {
	s := newTestAPIKeyStore(t)
	if s.Delete("nonexistent", "alice@test.com") {
		t.Error("Delete returned true for non-existent key")
	}
}

func TestAPIKeyFileStore_DeleteByEmail(t *testing.T) {
	s := newTestAPIKeyStore(t)
	s.Create(makeAPIKey("id1", "h1", "alice@test.com"))
	s.Create(makeAPIKey("id2", "h2", "alice@test.com"))
	s.Create(makeAPIKey("id3", "h3", "bob@test.com"))

	s.DeleteByEmail("alice@test.com")

	aliceKeys := s.List("alice@test.com")
	if len(aliceKeys) != 0 {
		t.Errorf("List(alice) len = %d after DeleteByEmail, want 0", len(aliceKeys))
	}

	bobKeys := s.List("bob@test.com")
	if len(bobKeys) != 1 {
		t.Errorf("List(bob) len = %d, want 1 (should be unaffected)", len(bobKeys))
	}
}

func TestAPIKeyFileStore_UpdateLastUsed(t *testing.T) {
	s := newTestAPIKeyStore(t)
	key := makeAPIKey("id1", "h1", "alice@test.com")
	s.Create(key)

	if key.LastUsedAt != nil {
		t.Fatal("LastUsedAt should be nil initially")
	}

	s.UpdateLastUsed("id1")

	got, ok := s.GetByHash("h1")
	if !ok {
		t.Fatal("GetByHash returned false")
	}
	if got.LastUsedAt == nil {
		t.Fatal("LastUsedAt should not be nil after update")
	}
}

func TestAPIKeyFileStore_UpdateLastUsed_NotFound(t *testing.T) {
	s := newTestAPIKeyStore(t)
	// Should not panic
	s.UpdateLastUsed("nonexistent")
}

func TestAPIKeyFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "apikeys.json")

	s1, err := NewAPIKeyFileStore(path, testLogger())
	if err != nil {
		t.Fatalf("NewAPIKeyFileStore: %v", err)
	}
	s1.Create(makeAPIKey("id1", "h1", "alice@test.com"))

	// Reload from disk
	s2, err := NewAPIKeyFileStore(path, testLogger())
	if err != nil {
		t.Fatalf("NewAPIKeyFileStore reload: %v", err)
	}

	got, ok := s2.GetByHash("h1")
	if !ok {
		t.Fatal("key not found after reload")
	}
	if got.ID != "id1" {
		t.Errorf("ID = %q, want %q", got.ID, "id1")
	}
}

func TestAPIKeyFileStore_ConcurrentAccess(t *testing.T) {
	s := newTestAPIKeyStore(t)
	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "id" + string(rune('A'+i))
			hash := "hash" + string(rune('A'+i))
			s.Create(makeAPIKey(id, hash, "user@test.com"))
			s.GetByHash(hash)
			s.List("user@test.com")
		}(i)
	}
	wg.Wait()

	keys := s.List("user@test.com")
	if len(keys) != 10 {
		t.Errorf("List() len = %d, want 10 after concurrent writes", len(keys))
	}
}
