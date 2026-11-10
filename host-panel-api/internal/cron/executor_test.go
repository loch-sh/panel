package cron

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTruncate(t *testing.T) {
	t.Run("ShortString", func(t *testing.T) {
		result := truncate("hello", 100)
		if result != "hello" {
			t.Errorf("expected 'hello', got %q", result)
		}
	})

	t.Run("ExactLength", func(t *testing.T) {
		result := truncate("hello", 5)
		if result != "hello" {
			t.Errorf("expected 'hello', got %q", result)
		}
	})

	t.Run("Truncated", func(t *testing.T) {
		result := truncate("hello world", 5)
		if result != "hello... [truncated]" {
			t.Errorf("expected 'hello... [truncated]', got %q", result)
		}
	})

	t.Run("EmptyString", func(t *testing.T) {
		result := truncate("", 10)
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})
}

func TestExecutor_RunHTTP_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected method GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	}))
	defer server.Close()

	store := newTestStore(t)
	executor := &Executor{
		httpClient: server.Client(),
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}

	cfg := &HTTPConfig{
		Method:  "GET",
		URL:     server.URL,
		Timeout: 5,
	}
	output, err := executor.runHTTP(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error : %v", err)
	}
	if !strings.Contains(output, "HTTP 200") {
		t.Errorf("expected 'HTTP 200' in output, got %q", output)
	}
	if !strings.Contains(output, "OK") {
		t.Errorf("expected 'OK' in output, got %q", output)
	}
}

func TestExecutor_RunHTTP_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "Internal Server Error")
	}))
	defer server.Close()

	store := newTestStore(t)
	executor := &Executor{
		httpClient: server.Client(),
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}

	cfg := &HTTPConfig{
		Method:  "GET",
		URL:     server.URL,
		Timeout: 5,
	}
	output, err := executor.runHTTP(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected an error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("expected 'HTTP 500' in error, got %q", err.Error())
	}
	if !strings.Contains(output, "Internal Server Error") {
		t.Errorf("expected response body in output, got %q", output)
	}
}

func TestExecutor_RunHTTP_PostWithBody(t *testing.T) {
	var receivedBody string
	var receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		receivedContentType = r.Header.Get("Content-Type")
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, "Created")
	}))
	defer server.Close()

	store := newTestStore(t)
	executor := &Executor{
		httpClient: server.Client(),
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}

	cfg := &HTTPConfig{
		Method:  "POST",
		URL:     server.URL,
		Body:    `{"key":"value"}`,
		Headers: map[string]string{"Content-Type": "application/json"},
		Timeout: 5,
	}
	output, err := executor.runHTTP(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error : %v", err)
	}
	if !strings.Contains(output, "HTTP 201") {
		t.Errorf("expected 'HTTP 201' in output, got %q", output)
	}
	if receivedBody != `{"key":"value"}` {
		t.Errorf("expected sent body, got %q", receivedBody)
	}
	if receivedContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", receivedContentType)
	}
}

func TestExecutor_RunHTTP_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := newTestStore(t)
	executor := &Executor{
		httpClient: server.Client(),
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}

	cfg := &HTTPConfig{
		Method:  "GET",
		URL:     server.URL,
		Timeout: 1,
	}
	_, err := executor.runHTTP(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestExecutor_RunHTTP_InvalidURL(t *testing.T) {
	store := newTestStore(t)
	executor := &Executor{
		httpClient: http.DefaultClient,
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}

	cfg := &HTTPConfig{
		Method:  "GET",
		URL:     "http://localhost:1", // closed port
		Timeout: 1,
	}
	_, err := executor.runHTTP(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected an error for unreachable URL")
	}
}

func TestExecutor_Run_HTTPJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "response body")
	}))
	defer server.Close()

	store := newTestStore(t)
	executor := &Executor{
		httpClient: server.Client(),
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}

	job := &Job{
		ID:         "test-run",
		Name:       "Test Run",
		Schedule:   "* * * * *",
		Type:       JobTypeHTTP,
		Enabled:    true,
		HTTP:       &HTTPConfig{Method: "GET", URL: server.URL, Timeout: 5},
		LastStatus: JobStatusRunning,
	}
	store.Set(job.ID, job)

	executor.Run(context.Background(), job)

	got, ok := store.Get("test-run")
	if !ok {
		t.Fatal("job should exist in the store")
	}
	if got.LastStatus != JobStatusSuccess {
		t.Errorf("expected status 'success', got %q", got.LastStatus)
	}
	if got.LastRun == nil {
		t.Error("LastRun should be set")
	}
	if !strings.Contains(got.LastOutput, "response body") {
		t.Errorf("expected 'response body' in LastOutput, got %q", got.LastOutput)
	}
	if got.LastError != "" {
		t.Errorf("expected no error, got %q", got.LastError)
	}
}

func TestExecutor_Run_HTTPJob_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "bad gateway")
	}))
	defer server.Close()

	store := newTestStore(t)
	executor := &Executor{
		httpClient: server.Client(),
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}

	job := &Job{
		ID:         "test-err",
		Name:       "Test Err",
		Schedule:   "* * * * *",
		Type:       JobTypeHTTP,
		Enabled:    true,
		HTTP:       &HTTPConfig{Method: "GET", URL: server.URL, Timeout: 5},
		LastStatus: JobStatusRunning,
	}
	store.Set(job.ID, job)

	executor.Run(context.Background(), job)

	got, _ := store.Get("test-err")
	if got.LastStatus != JobStatusError {
		t.Errorf("expected status 'error', got %q", got.LastStatus)
	}
	if got.LastError == "" {
		t.Error("LastError should contain the error message")
	}
	if !strings.Contains(got.LastOutput, "bad gateway") {
		t.Errorf("expected 'bad gateway' in LastOutput, got %q", got.LastOutput)
	}
}

func TestExecutor_Run_UnknownType(t *testing.T) {
	store := newTestStore(t)
	executor := &Executor{
		httpClient: http.DefaultClient,
		store:      store,
		logger:     testLogger(),
		audit:      testLogger(),
	}

	job := &Job{
		ID:         "test-unknown",
		Name:       "Unknown",
		Schedule:   "* * * * *",
		Type:       "unknown",
		Enabled:    true,
		LastStatus: JobStatusRunning,
	}
	store.Set(job.ID, job)

	executor.Run(context.Background(), job)

	got, _ := store.Get("test-unknown")
	if got.LastStatus != JobStatusError {
		t.Errorf("expected status 'error', got %q", got.LastStatus)
	}
	if !strings.Contains(got.LastError, "unknown job type") {
		t.Errorf("expected unknown type error, got %q", got.LastError)
	}
}

func TestAuditErrorHidesURL(t *testing.T) {
	uerr := &url.Error{Op: "Get", URL: "https://user:pass@example.com/hook?token=SECRET", Err: errors.New("connection refused")}
	for _, err := range []error{uerr, fmt.Errorf("HTTP request: %w", uerr)} {
		got := auditError(err)
		if strings.Contains(got, "SECRET") || strings.Contains(got, "pass") || !strings.Contains(got, "connection refused") {
			t.Fatalf("auditError(%v) = %q", err, got)
		}
	}
}
