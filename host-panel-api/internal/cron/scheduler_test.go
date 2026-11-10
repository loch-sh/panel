package cron

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGenerateID(t *testing.T) {
	t.Run("Length", func(t *testing.T) {
		id := GenerateID()
		// 16 bytes → 32 hex characters
		if len(id) != 32 {
			t.Errorf("expected 32 characters, got %d (%q)", len(id), id)
		}
	})

	t.Run("HexOnly", func(t *testing.T) {
		id := GenerateID()
		for _, c := range id {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Errorf("non-hex character found: %c in %q", c, id)
				break
			}
		}
	})

	t.Run("Unique", func(t *testing.T) {
		seen := make(map[string]bool)
		for i := 0; i < 1000; i++ {
			id := GenerateID()
			if seen[id] {
				t.Fatalf("duplicate detected: %q", id)
			}
			seen[id] = true
		}
	})
}

// newTestScheduler creates a complete scheduler with a temporary store and an
// executor using a test HTTP server.
func newTestScheduler(t *testing.T, server *httptest.Server) (*Scheduler, *Store) {
	t.Helper()
	store := newTestStore(t)
	executor := &Executor{
		httpClient: server.Client(),
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}
	scheduler := NewScheduler(store, executor, testLogger())
	return scheduler, store
}

func TestScheduler_AddAndRemoveJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, _ := newTestScheduler(t, server)
	scheduler.cron.Start()
	defer scheduler.cron.Stop()

	job := &Job{
		ID:       "test-add",
		Name:     "Test Add",
		Schedule: "@every 1h",
		Type:     JobTypeHTTP,
		Enabled:  true,
		HTTP:     &HTTPConfig{Method: "GET", URL: server.URL},
	}

	if err := scheduler.AddJob(job); err != nil {
		t.Fatalf("AddJob error : %v", err)
	}

	scheduler.mu.Lock()
	_, exists := scheduler.entries["test-add"]
	scheduler.mu.Unlock()
	if !exists {
		t.Error("job should be registered in the scheduler")
	}

	scheduler.RemoveJob("test-add")

	scheduler.mu.Lock()
	_, exists = scheduler.entries["test-add"]
	scheduler.mu.Unlock()
	if exists {
		t.Error("job should no longer be registered after removal")
	}
}

func TestScheduler_AddJob_DisabledSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, _ := newTestScheduler(t, server)

	job := &Job{
		ID:       "disabled",
		Name:     "Disabled",
		Schedule: "@every 1h",
		Type:     JobTypeHTTP,
		Enabled:  false,
		HTTP:     &HTTPConfig{Method: "GET", URL: server.URL},
	}

	if err := scheduler.AddJob(job); err != nil {
		t.Fatalf("AddJob error : %v", err)
	}

	scheduler.mu.Lock()
	_, exists := scheduler.entries["disabled"]
	scheduler.mu.Unlock()
	if exists {
		t.Error("a disabled job should not be registered")
	}
}

func TestScheduler_AddJob_InvalidSchedule(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, _ := newTestScheduler(t, server)

	job := &Job{
		ID:       "invalid",
		Name:     "Invalid",
		Schedule: "not-a-cron",
		Type:     JobTypeHTTP,
		Enabled:  true,
		HTTP:     &HTTPConfig{Method: "GET", URL: server.URL},
	}

	if err := scheduler.AddJob(job); err == nil {
		t.Error("expected an error for an invalid schedule")
	}
}

func TestScheduler_UpdateJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, _ := newTestScheduler(t, server)
	scheduler.cron.Start()
	defer scheduler.cron.Stop()

	job := &Job{
		ID:       "test-update",
		Name:     "Before",
		Schedule: "@every 1h",
		Type:     JobTypeHTTP,
		Enabled:  true,
		HTTP:     &HTTPConfig{Method: "GET", URL: server.URL},
	}
	if err := scheduler.AddJob(job); err != nil {
		t.Fatal(err)
	}

	// Update with a new schedule
	updated := &Job{
		ID:       "test-update",
		Name:     "After",
		Schedule: "@every 30m",
		Type:     JobTypeHTTP,
		Enabled:  true,
		HTTP:     &HTTPConfig{Method: "GET", URL: server.URL},
	}
	if err := scheduler.UpdateJob(updated); err != nil {
		t.Fatalf("UpdateJob error: %v", err)
	}

	scheduler.mu.Lock()
	_, exists := scheduler.entries["test-update"]
	scheduler.mu.Unlock()
	if !exists {
		t.Error("updated job should be registered")
	}
}

func TestScheduler_UpdateJob_Disable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, _ := newTestScheduler(t, server)
	scheduler.cron.Start()
	defer scheduler.cron.Stop()

	job := &Job{
		ID:       "test-disable",
		Name:     "ToDisable",
		Schedule: "@every 1h",
		Type:     JobTypeHTTP,
		Enabled:  true,
		HTTP:     &HTTPConfig{Method: "GET", URL: server.URL},
	}
	if err := scheduler.AddJob(job); err != nil {
		t.Fatal(err)
	}

	// Disable
	job.Enabled = false
	if err := scheduler.UpdateJob(job); err != nil {
		t.Fatal(err)
	}

	scheduler.mu.Lock()
	_, exists := scheduler.entries["test-disable"]
	scheduler.mu.Unlock()
	if exists {
		t.Error("a disabled job should no longer be registered")
	}
}

func TestScheduler_IsRunning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, _ := newTestScheduler(t, server)

	if scheduler.IsRunning("any") {
		t.Error("no job should be running at startup")
	}

	// Simulate a running job
	scheduler.mu.Lock()
	scheduler.running["test-running"] = true
	scheduler.mu.Unlock()

	if !scheduler.IsRunning("test-running") {
		t.Error("job should be marked as running")
	}
}

func TestScheduler_ConcurrentExecutionPrevention(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		// Simulate a long-running job
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, store := newTestScheduler(t, server)

	job := &Job{
		ID:         "test-lock",
		Name:       "Lock Test",
		Schedule:   "* * * * *",
		Type:       JobTypeHTTP,
		Enabled:    true,
		HTTP:       &HTTPConfig{Method: "GET", URL: server.URL, Timeout: 5},
		LastStatus: JobStatusPending,
	}
	store.Set(job.ID, job)

	// Launch two executions in parallel
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scheduler.executeWithLock("test-lock")
	}()
	// Short delay so the first one acquires the lock
	time.Sleep(50 * time.Millisecond)
	go func() {
		defer wg.Done()
		scheduler.executeWithLock("test-lock")
	}()
	wg.Wait()

	mu.Lock()
	count := callCount
	mu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 execution (locking), got %d", count)
	}
}

func TestScheduler_RunNow(t *testing.T) {
	called := make(chan bool, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- true
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	}))
	defer server.Close()

	scheduler, store := newTestScheduler(t, server)

	job := &Job{
		ID:         "test-now",
		Name:       "Run Now",
		Schedule:   "@every 1h",
		Type:       JobTypeHTTP,
		Enabled:    true,
		HTTP:       &HTTPConfig{Method: "GET", URL: server.URL, Timeout: 5},
		LastStatus: JobStatusPending,
	}
	store.Set(job.ID, job)

	scheduler.RunNow(context.Background(), job)

	select {
	case <-called:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("RunNow did not trigger execution within the deadline")
	}

	// Wait for the goroutine to finish
	time.Sleep(100 * time.Millisecond)

	got, _ := store.Get("test-now")
	if got.LastStatus != JobStatusSuccess {
		t.Errorf("expected status 'success', got %q", got.LastStatus)
	}
}

func TestScheduler_OnceJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, store := newTestScheduler(t, server)
	scheduler.cron.Start()
	defer scheduler.cron.Stop()

	job := &Job{
		ID:         "test-once",
		Name:       "Once",
		Schedule:   "@every 1h",
		Type:       JobTypeHTTP,
		Enabled:    true,
		Once:       true,
		HTTP:       &HTTPConfig{Method: "GET", URL: server.URL, Timeout: 5},
		LastStatus: JobStatusPending,
	}
	store.Set(job.ID, job)
	if err := scheduler.AddJob(job); err != nil {
		t.Fatal(err)
	}

	// Execute manually
	scheduler.executeWithLock("test-once")

	got, _ := store.Get("test-once")
	if got.Enabled {
		t.Error("a 'once' job should be disabled after execution")
	}

	scheduler.mu.Lock()
	_, exists := scheduler.entries["test-once"]
	scheduler.mu.Unlock()
	if exists {
		t.Error("a 'once' job should be removed from the scheduler after execution")
	}
}

func TestScheduler_StartLoadsEnabledJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := newTestStore(t)
	now := time.Now()

	store.Set("enabled", &Job{
		ID: "enabled", Name: "Enabled", Schedule: "@every 1h",
		Type: JobTypeHTTP, Enabled: true,
		HTTP:       &HTTPConfig{Method: "GET", URL: server.URL},
		LastStatus: JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})
	store.Set("disabled", &Job{
		ID: "disabled", Name: "Disabled", Schedule: "@every 1h",
		Type: JobTypeHTTP, Enabled: false,
		HTTP:       &HTTPConfig{Method: "GET", URL: server.URL},
		LastStatus: JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	executor := &Executor{
		httpClient: server.Client(),
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}
	scheduler := NewScheduler(store, executor, testLogger())
	scheduler.Start()
	defer scheduler.Stop()

	scheduler.mu.Lock()
	_, enabledExists := scheduler.entries["enabled"]
	_, disabledExists := scheduler.entries["disabled"]
	scheduler.mu.Unlock()

	if !enabledExists {
		t.Error("enabled job should be loaded at startup")
	}
	if disabledExists {
		t.Error("disabled job should not be loaded")
	}
}

func TestScheduler_ExecuteWithLock_DisabledJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP server should not be called for a disabled job")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, store := newTestScheduler(t, server)

	job := &Job{
		ID:         "test-disabled-exec",
		Name:       "Disabled",
		Schedule:   "* * * * *",
		Type:       JobTypeHTTP,
		Enabled:    false,
		HTTP:       &HTTPConfig{Method: "GET", URL: server.URL},
		LastStatus: JobStatusPending,
	}
	store.Set(job.ID, job)

	// Execute — should do nothing since the job is disabled
	scheduler.executeWithLock("test-disabled-exec")

	got, _ := store.Get("test-disabled-exec")
	if got.LastStatus != JobStatusPending {
		t.Errorf("status should not change for a disabled job, got %q", got.LastStatus)
	}
}

func TestScheduler_ExecuteWithLock_MissingJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP server should not be called for a non-existent job")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scheduler, _ := newTestScheduler(t, server)

	// Should not panic
	scheduler.executeWithLock("inexistant")

	if scheduler.IsRunning("inexistant") {
		t.Error("lock should be released even for a non-existent job")
	}
}

func TestScheduler_ExecuteWithLock_OutputInStore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "job output here")
	}))
	defer server.Close()

	scheduler, store := newTestScheduler(t, server)

	job := &Job{
		ID:         "test-output",
		Name:       "Output",
		Schedule:   "* * * * *",
		Type:       JobTypeHTTP,
		Enabled:    true,
		HTTP:       &HTTPConfig{Method: "GET", URL: server.URL, Timeout: 5},
		LastStatus: JobStatusPending,
	}
	store.Set(job.ID, job)

	scheduler.executeWithLock("test-output")

	got, _ := store.Get("test-output")
	if got.LastStatus != JobStatusSuccess {
		t.Errorf("expected 'success', got %q", got.LastStatus)
	}
	if !strings.Contains(got.LastOutput, "job output here") {
		t.Errorf("expected output in LastOutput, got %q", got.LastOutput)
	}
	if got.LastRun == nil {
		t.Error("LastRun should be set after execution")
	}
}
