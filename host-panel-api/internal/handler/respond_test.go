package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 200, map[string]string{"message": "ok"})

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["message"] != "ok" {
		t.Errorf("message = %q, want %q", body["message"], "ok")
	}
}

func TestWriteJSON_StatusCreated(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 201, map[string]string{"id": "123"})

	if w.Code != 201 {
		t.Errorf("status = %d, want 201", w.Code)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, 400, "INVALID_REQUEST_BODY", "bad request")

	resp := w.Result()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}

	var body map[string]string
	decodeJSON(t, resp.Body, &body)

	if body["code"] != "INVALID_REQUEST_BODY" {
		t.Errorf("code = %q, want %q", body["code"], "INVALID_REQUEST_BODY")
	}
	if body["error"] != "bad request" {
		t.Errorf("error = %q, want %q", body["error"], "bad request")
	}
}

func TestWriteJSON_EmptySlice(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 200, []string{})

	var body []string
	decodeJSON(t, w.Result().Body, &body)
	if body == nil {
		t.Error("empty slice should serialize as [] not null")
	}
}
