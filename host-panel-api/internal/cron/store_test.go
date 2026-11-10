package cron

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cron.json")
	s, err := NewStore(path, testLogger())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	return s
}

func makeJob(id, name string, enabled bool, createdAt time.Time) *Job {
	return &Job{
		ID:         id,
		Name:       name,
		Schedule:   "* * * * *",
		Type:       JobTypeHTTP,
		Enabled:    enabled,
		HTTP:       &HTTPConfig{Method: "GET", URL: "http://example.com"},
		LastStatus: JobStatusPending,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	}
}

func TestNewStore_EmptyFile(t *testing.T) {
	s := newTestStore(t)
	if len(s.List()) != 0 {
		t.Error("a new store should be empty")
	}
}

func TestNewStore_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.json")

	jobs := map[string]*Job{
		"job1": makeJob("job1", "Test", true, time.Now()),
	}
	data, _ := json.MarshalIndent(jobs, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(path, testLogger())
	if err != nil {
		t.Fatalf("failed to load store: %v", err)
	}
	if len(s.List()) != 1 {
		t.Errorf("expected 1 job, got %d", len(s.List()))
	}
}

func TestNewStore_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.json")
	if err := os.WriteFile(path, []byte("{invalid json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := NewStore(path, testLogger())
	if err == nil {
		t.Error("a corrupted JSON file should cause an error")
	}
}

func TestNewStore_EmptyFileContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.json")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(path, testLogger())
	if err != nil {
		t.Fatalf("an empty file should not cause an error: %v", err)
	}
	if len(s.List()) != 0 {
		t.Error("an empty file should produce an empty store")
	}
}

func TestStore_SetAndGet(t *testing.T) {
	s := newTestStore(t)
	job := makeJob("job1", "Mon job", true, time.Now())

	s.Set("job1", job)

	got, ok := s.Get("job1")
	if !ok {
		t.Fatal("job should exist")
	}
	if got.Name != "Mon job" {
		t.Errorf("expected name 'Mon job', got %q", got.Name)
	}
}

func TestStore_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, ok := s.Get("inexistant")
	if ok {
		t.Error("a non-existent job should not be found")
	}
}

func TestStore_Delete(t *testing.T) {
	s := newTestStore(t)
	s.Set("job1", makeJob("job1", "Test", true, time.Now()))

	if !s.Delete("job1") {
		t.Error("deleting an existing job should return true")
	}

	_, ok := s.Get("job1")
	if ok {
		t.Error("deleted job should no longer exist")
	}
}

func TestStore_DeleteNotFound(t *testing.T) {
	s := newTestStore(t)
	if s.Delete("inexistant") {
		t.Error("deleting a non-existent job should return false")
	}
}

func TestStore_List_SortedByCreatedAtDesc(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	s.Set("old", makeJob("old", "Ancien", true, now.Add(-2*time.Hour)))
	s.Set("mid", makeJob("mid", "Milieu", true, now.Add(-1*time.Hour)))
	s.Set("new", makeJob("new", "Récent", true, now))

	jobs := s.List()
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
	if jobs[0].ID != "new" || jobs[1].ID != "mid" || jobs[2].ID != "old" {
		t.Errorf("incorrect order: %s, %s, %s", jobs[0].ID, jobs[1].ID, jobs[2].ID)
	}
}

func TestStore_ListEnabled(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	s.Set("enabled1", makeJob("enabled1", "Activé 1", true, now))
	s.Set("disabled", makeJob("disabled", "Désactivé", false, now))
	s.Set("enabled2", makeJob("enabled2", "Activé 2", true, now))

	enabled := s.ListEnabled()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled jobs, got %d", len(enabled))
	}
	for _, j := range enabled {
		if !j.Enabled {
			t.Errorf("job %q should not be in the enabled list", j.ID)
		}
	}
}

func TestStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.json")

	// Create and populate a store
	s1, _ := NewStore(path, testLogger())
	s1.Set("job1", makeJob("job1", "Persistant", true, time.Now()))

	// Reload from file
	s2, err := NewStore(path, testLogger())
	if err != nil {
		t.Fatalf("failed to reload store: %v", err)
	}

	got, ok := s2.Get("job1")
	if !ok {
		t.Fatal("job should persist after reload")
	}
	if got.Name != "Persistant" {
		t.Errorf("expected name 'Persistant', got %q", got.Name)
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := newTestStore(t)
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := GenerateID()
			s.Set(id, makeJob(id, "Concurrent", true, time.Now()))
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.List()
		}()
	}

	wg.Wait()

	jobs := s.List()
	if len(jobs) != 50 {
		t.Errorf("expected 50 jobs, got %d", len(jobs))
	}
}

func TestStore_Update(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	s.Set("job1", makeJob("job1", "Original", true, now))

	updated := makeJob("job1", "Modified", false, now)
	s.Set("job1", updated)

	got, _ := s.Get("job1")
	if got.Name != "Modified" {
		t.Errorf("expected name 'Modified', got %q", got.Name)
	}
	if got.Enabled {
		t.Error("job should be disabled")
	}

	// Verify there is only one job
	if len(s.List()) != 1 {
		t.Errorf("expected 1 job after update, got %d", len(s.List()))
	}
}
