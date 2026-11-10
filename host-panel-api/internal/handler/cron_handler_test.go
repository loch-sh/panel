package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"host-panel-api/internal/cron"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"
)

const testHTTPTimeout = 900 // 15 min

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestCronHandler(t *testing.T) *CronHandler {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cron.json")
	store, err := cron.NewStore(path, testLogger())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	executor := cron.NewExecutor(nil, store, testLogger(), testLogger(), 15*time.Minute)
	scheduler := cron.NewScheduler(store, executor, testLogger())
	scheduler.Start()
	t.Cleanup(func() { scheduler.Stop() })

	return &CronHandler{
		Store:       store,
		Scheduler:   scheduler,
		Logger:      testLogger(),
		Audit:       testLogger(),
		MaxJobs:     50,
		HTTPTimeout: testHTTPTimeout,
	}
}

func adminCtx(r *http.Request) *http.Request {
	ctx := middleware.WithUser(r.Context(), &model.User{
		Email:   "admin@test.com",
		Name:    "Admin",
		Profile: model.ProfileAdmin,
	})
	return r.WithContext(ctx)
}

func restrictedCtx(r *http.Request, permissions []string) *http.Request {
	ctx := middleware.WithUser(r.Context(), &model.User{
		Email:       "user@test.com",
		Name:        "User",
		Profile:     model.ProfileRestricted,
		Permissions: permissions,
	})
	return r.WithContext(ctx)
}

func decodeJSON(t *testing.T, body io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
}

// --- Create ---

func TestCronHandler_Create_Success(t *testing.T) {
	h := newTestCronHandler(t)

	payload := map[string]any{
		"name":     "Test HTTP",
		"schedule": "0 * * * *",
		"type":     "http",
		"enabled":  true,
		"once":     false,
		"http": map[string]any{
			"method":  "GET",
			"url":     "http://example.com",
			"timeout": 30,
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/cron", bytes.NewReader(body))
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Create(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d : %s", rr.Code, rr.Body.String())
	}

	var result cron.Job
	decodeJSON(t, rr.Body, &result)

	if result.ID == "" {
		t.Error("ID should be generated")
	}
	if result.Name != "Test HTTP" {
		t.Errorf("expected name 'Test HTTP', got %q", result.Name)
	}
	if result.LastStatus != cron.JobStatusPending {
		t.Errorf("expected status 'pending', got %q", result.LastStatus)
	}
	if result.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestCronHandler_Create_InvalidJSON(t *testing.T) {
	h := newTestCronHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/cron", bytes.NewReader([]byte("{invalid")))
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Create(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestCronHandler_Create_ValidationError(t *testing.T) {
	h := newTestCronHandler(t)

	payload := map[string]any{
		"name":     "",
		"schedule": "* * * * *",
		"type":     "http",
		"http":     map[string]any{"method": "GET", "url": "http://example.com"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/cron", bytes.NewReader(body))
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Create(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d : %s", rr.Code, rr.Body.String())
	}
}

func TestCronHandler_Create_InvalidCronExpression(t *testing.T) {
	h := newTestCronHandler(t)

	payload := map[string]any{
		"name":     "Test",
		"schedule": "not-valid",
		"type":     "http",
		"enabled":  true,
		"http":     map[string]any{"method": "GET", "url": "http://example.com"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/cron", bytes.NewReader(body))
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Create(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d : %s", rr.Code, rr.Body.String())
	}
}

func TestCronHandler_Create_NoAuth(t *testing.T) {
	h := newTestCronHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/cron", nil)
	rr := httptest.NewRecorder()

	h.Create(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestCronHandler_Create_RestrictedUserHTTP(t *testing.T) {
	h := newTestCronHandler(t)

	payload := map[string]any{
		"name":     "HTTP Restricted",
		"schedule": "@every 5m",
		"type":     "http",
		"enabled":  true,
		"once":     false,
		"http":     map[string]any{"method": "GET", "url": "http://example.com"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/cron", bytes.NewReader(body))
	req = restrictedCtx(req, nil)
	rr := httptest.NewRecorder()

	h.Create(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("a restricted user should be able to create an HTTP job, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCronHandler_Create_UnknownField(t *testing.T) {
	h := newTestCronHandler(t)

	payload := map[string]any{
		"name":         "Test",
		"schedule":     "* * * * *",
		"type":         "http",
		"enabled":      true,
		"unknownField": "value",
		"http":         map[string]any{"method": "GET", "url": "http://example.com"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/cron", bytes.NewReader(body))
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Create(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("an unknown field should cause a 400, got %d", rr.Code)
	}
}

// --- List ---

func TestCronHandler_List_Admin(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Job 1", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com"},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})
	h.Store.Set("j2", &cron.Job{
		ID: "j2", Name: "Job 2", Schedule: "* * * * *",
		Type: cron.JobTypeDockerExec, Enabled: true,
		DockerExec: &cron.DockerExecConfig{ContainerName: "my-container", Command: []string{"ls"}},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	req := httptest.NewRequest(http.MethodGet, "/cron", nil)
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.List(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var jobs []*cron.Job
	decodeJSON(t, rr.Body, &jobs)

	if len(jobs) != 2 {
		t.Errorf("admin should see 2 jobs, got %d", len(jobs))
	}
}

func TestCronHandler_List_RestrictedSeesHTTPJobs(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("http1", &cron.Job{
		ID: "http1", Name: "HTTP 1", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com"},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})
	h.Store.Set("http2", &cron.Job{
		ID: "http2", Name: "HTTP 2", Schedule: "0 * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "POST", URL: "http://example.com/api"},
		LastStatus: cron.JobStatusPending, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	})

	req := httptest.NewRequest(http.MethodGet, "/cron", nil)
	req = restrictedCtx(req, nil)
	rr := httptest.NewRecorder()

	h.List(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var jobs []*cron.Job
	decodeJSON(t, rr.Body, &jobs)

	if len(jobs) != 2 {
		t.Errorf("restricted user should see 2 HTTP jobs, got %d", len(jobs))
	}
	for _, j := range jobs {
		if j.Type != cron.JobTypeHTTP {
			t.Errorf("all visible jobs should be HTTP, got %q", j.Type)
		}
	}
}

// --- Get ---

func TestCronHandler_Get_Success(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Mon Job", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com"},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	req := httptest.NewRequest(http.MethodGet, "/cron/j1", nil)
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Get(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var job cron.Job
	decodeJSON(t, rr.Body, &job)
	if job.Name != "Mon Job" {
		t.Errorf("expected 'Mon Job', got %q", job.Name)
	}
}

func TestCronHandler_Get_NotFound(t *testing.T) {
	h := newTestCronHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/cron/inexistant", nil)
	req.SetPathValue("id", "inexistant")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Get(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- Update ---

func TestCronHandler_Update_Success(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()
	created := now.Add(-1 * time.Hour)

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Original", Schedule: "@every 1h",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com"},
		LastStatus: cron.JobStatusSuccess, CreatedAt: created, UpdatedAt: created,
	})

	payload := map[string]any{
		"name":     "Modified",
		"schedule": "@every 30m",
		"type":     "http",
		"enabled":  true,
		"once":     false,
		"http":     map[string]any{"method": "POST", "url": "http://example.com/new"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPut, "/cron/j1", bytes.NewReader(body))
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Update(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d : %s", rr.Code, rr.Body.String())
	}

	var result cron.Job
	decodeJSON(t, rr.Body, &result)

	if result.Name != "Modified" {
		t.Errorf("expected name 'Modified', got %q", result.Name)
	}
	if result.ID != "j1" {
		t.Errorf("ID should not change, got %q", result.ID)
	}
	// CreatedAt must be preserved
	if result.CreatedAt.IsZero() {
		t.Error("CreatedAt should be preserved")
	}
	// LastStatus must be preserved
	if result.LastStatus != cron.JobStatusSuccess {
		t.Errorf("LastStatus should be preserved, got %q", result.LastStatus)
	}
}

func TestCronHandler_Update_NotFound(t *testing.T) {
	h := newTestCronHandler(t)

	payload := map[string]any{
		"name":     "Test",
		"schedule": "* * * * *",
		"type":     "http",
		"enabled":  true,
		"http":     map[string]any{"method": "GET", "url": "http://example.com"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPut, "/cron/inexistant", bytes.NewReader(body))
	req.SetPathValue("id", "inexistant")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Update(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- Delete ---

func TestCronHandler_Delete_Success(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "À supprimer", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com"},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	req := httptest.NewRequest(http.MethodDelete, "/cron/j1", nil)
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Delete(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}

	_, ok := h.Store.Get("j1")
	if ok {
		t.Error("job should be deleted from the store")
	}
}

func TestCronHandler_Delete_NotFound(t *testing.T) {
	h := newTestCronHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/cron/inexistant", nil)
	req.SetPathValue("id", "inexistant")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Delete(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- RunNow ---

func TestCronHandler_RunNow_Success(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Run Now", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com"},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	req := httptest.NewRequest(http.MethodPost, "/cron/j1/run", nil)
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.RunNow(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d : %s", rr.Code, rr.Body.String())
	}

	// Wait for the goroutine launched by RunNow to finish
	time.Sleep(200 * time.Millisecond)
}

func TestCronHandler_RunNow_NotFound(t *testing.T) {
	h := newTestCronHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/cron/inexistant/run", nil)
	req.SetPathValue("id", "inexistant")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.RunNow(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestCronHandler_RunNow_Accepted(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Running", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com"},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	req := httptest.NewRequest(http.MethodPost, "/cron/j1/run", nil)
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.RunNow(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", rr.Code)
	}

	// Wait for the goroutine launched by RunNow to finish
	time.Sleep(200 * time.Millisecond)
}

// --- Patch ---

func TestCronHandler_Patch_ToggleEnabled(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Mon Job", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com", Timeout: testHTTPTimeout},
		LastStatus: cron.JobStatusSuccess, CreatedAt: now, UpdatedAt: now,
	})

	payload := map[string]any{"enabled": false}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/cron/j1", bytes.NewReader(body))
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Patch(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d : %s", rr.Code, rr.Body.String())
	}

	var result cron.Job
	decodeJSON(t, rr.Body, &result)

	if result.Enabled {
		t.Error("job should be disabled")
	}
	if result.Name != "Mon Job" {
		t.Errorf("name should not change, got %q", result.Name)
	}
	if result.LastStatus != cron.JobStatusSuccess {
		t.Errorf("LastStatus should be preserved, got %q", result.LastStatus)
	}
}

func TestCronHandler_Patch_UpdateName(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Ancien", Schedule: "@every 5m",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com", Timeout: testHTTPTimeout},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	payload := map[string]any{"name": "Nouveau"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/cron/j1", bytes.NewReader(body))
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Patch(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d : %s", rr.Code, rr.Body.String())
	}

	var result cron.Job
	decodeJSON(t, rr.Body, &result)

	if result.Name != "Nouveau" {
		t.Errorf("expected name 'Nouveau', got %q", result.Name)
	}
	if result.Schedule != "@every 5m" {
		t.Errorf("schedule should not change, got %q", result.Schedule)
	}
}

func TestCronHandler_Patch_UpdateSchedule(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Test", Schedule: "@every 5m",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com", Timeout: testHTTPTimeout},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	payload := map[string]any{"schedule": "0 */2 * * *"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/cron/j1", bytes.NewReader(body))
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Patch(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d : %s", rr.Code, rr.Body.String())
	}

	var result cron.Job
	decodeJSON(t, rr.Body, &result)

	if result.Schedule != "0 */2 * * *" {
		t.Errorf("expected schedule '0 */2 * * *', got %q", result.Schedule)
	}
}

func TestCronHandler_Patch_NotFound(t *testing.T) {
	h := newTestCronHandler(t)

	payload := map[string]any{"enabled": false}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/cron/inexistant", bytes.NewReader(body))
	req.SetPathValue("id", "inexistant")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Patch(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestCronHandler_Patch_InvalidSchedule(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Test", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com", Timeout: testHTTPTimeout},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	payload := map[string]any{"schedule": "not-valid"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/cron/j1", bytes.NewReader(body))
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Patch(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d : %s", rr.Code, rr.Body.String())
	}
}

func TestCronHandler_Patch_TooFrequent(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Test", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com", Timeout: testHTTPTimeout},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	payload := map[string]any{"schedule": "@every 10s"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/cron/j1", bytes.NewReader(body))
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Patch(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d : %s", rr.Code, rr.Body.String())
	}
}

func TestCronHandler_Patch_EmptyBody(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Inchangé", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com", Timeout: testHTTPTimeout},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	payload := map[string]any{}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/cron/j1", bytes.NewReader(body))
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Patch(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d : %s", rr.Code, rr.Body.String())
	}

	var result cron.Job
	decodeJSON(t, rr.Body, &result)

	if result.Name != "Inchangé" {
		t.Errorf("expected name 'Inchangé', got %q", result.Name)
	}
}

func TestCronHandler_Patch_PreservesServerFields(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()
	lastRun := now.Add(-1 * time.Hour)

	h.Store.Set("j1", &cron.Job{
		ID: "j1", Name: "Test", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com", Timeout: testHTTPTimeout},
		LastStatus: cron.JobStatusSuccess, LastRun: &lastRun,
		LastError: "ancienne erreur", LastOutput: "ancienne sortie",
		CreatedAt: now, UpdatedAt: now,
	})

	payload := map[string]any{"name": "Renommé"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, "/cron/j1", bytes.NewReader(body))
	req.SetPathValue("id", "j1")
	req = adminCtx(req)
	rr := httptest.NewRecorder()

	h.Patch(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d : %s", rr.Code, rr.Body.String())
	}

	var result cron.Job
	decodeJSON(t, rr.Body, &result)

	if result.LastStatus != cron.JobStatusSuccess {
		t.Errorf("LastStatus should be preserved, got %q", result.LastStatus)
	}
	if result.LastError != "ancienne erreur" {
		t.Errorf("LastError should be preserved, got %q", result.LastError)
	}
	if result.LastOutput != "ancienne sortie" {
		t.Errorf("LastOutput should be preserved, got %q", result.LastOutput)
	}
	if result.CreatedAt.IsZero() {
		t.Error("CreatedAt should be preserved")
	}
}

// --- Permissions ---

// Note: Docker permission tests for restricted users require
// a real Docker client (UserCanAccessContainerByID calls InspectContainer).
// These scenarios are tested in integration tests, not unit tests.

func TestCronHandler_RestrictedUser_CanAccessHTTPJob(t *testing.T) {
	h := newTestCronHandler(t)
	now := time.Now()

	h.Store.Set("http-job", &cron.Job{
		ID: "http-job", Name: "HTTP", Schedule: "* * * * *",
		Type: cron.JobTypeHTTP, Enabled: true,
		HTTP:       &cron.HTTPConfig{Method: "GET", URL: "http://example.com"},
		LastStatus: cron.JobStatusPending, CreatedAt: now, UpdatedAt: now,
	})

	req := httptest.NewRequest(http.MethodGet, "/cron/http-job", nil)
	req.SetPathValue("id", "http-job")
	req = restrictedCtx(req, nil)
	rr := httptest.NewRecorder()

	h.Get(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("a restricted user should be able to access HTTP jobs, got %d", rr.Code)
	}
}
