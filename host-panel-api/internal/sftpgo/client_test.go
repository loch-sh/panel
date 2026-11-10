package sftpgo

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestClient_ListUsers_SendsAPIKeyHeader(t *testing.T) {
	var gotKey, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get(apiKeyHeader)
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode([]User{{Username: "a"}, {Username: "b"}})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret-key", testLogger())
	users, err := c.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers() error: %v", err)
	}
	if gotKey != "secret-key" {
		t.Errorf("%s header = %q, want secret-key", apiKeyHeader, gotKey)
	}
	if gotPath != "/api/v2/users" {
		t.Errorf("path = %q, want /api/v2/users", gotPath)
	}
	if len(users) != 2 {
		t.Errorf("got %d users, want 2", len(users))
	}
}

func TestClient_CreateUser_SendsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var u User
		json.NewDecoder(r.Body).Decode(&u)
		if u.Username != "client1" {
			t.Errorf("username = %q, want client1", u.Username)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(u)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k", testLogger())
	created, err := c.CreateUser(context.Background(), &User{Username: "client1", HomeDir: "/data/x"})
	if err != nil {
		t.Fatalf("CreateUser() error: %v", err)
	}
	if created.Username != "client1" {
		t.Errorf("created username = %q", created.Username)
	}
}

func TestClient_GetUser_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k", testLogger())
	_, err := c.GetUser(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !NotFound(err) {
		t.Errorf("NotFound(err) = false, want true (err: %v)", err)
	}
}

func TestClient_DeleteUser_EscapesUsername(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k", testLogger())
	if err := c.DeleteUser(context.Background(), "a b"); err != nil {
		t.Fatalf("DeleteUser() error: %v", err)
	}
	if gotPath != "/api/v2/users/a%20b" {
		t.Errorf("path = %q, want /api/v2/users/a%%20b", gotPath)
	}
}
