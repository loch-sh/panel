package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testSecret = "super-secret-key-for-testing-1234"

func TestCookieManager_SetAndGetSigned(t *testing.T) {
	cm := NewCookieManager(testSecret)
	opts := CookieOptions{Path: "/", MaxAge: 3600}

	// Set a signed cookie
	w := httptest.NewRecorder()
	cm.SetSigned(w, "token", "my-jwt-value", opts)

	// Read it back via a new request with the cookie
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}

	value, err := cm.GetSigned(r, "token")
	if err != nil {
		t.Fatalf("GetSigned() error: %v", err)
	}
	if value != "my-jwt-value" {
		t.Errorf("value = %q, want %q", value, "my-jwt-value")
	}
}

func TestCookieManager_TamperedValue(t *testing.T) {
	cm := NewCookieManager(testSecret)
	opts := CookieOptions{Path: "/"}

	w := httptest.NewRecorder()
	cm.SetSigned(w, "token", "original", opts)

	// Tamper with the cookie value
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	cookies := w.Result().Cookies()
	cookies[0].Value = "tampered.signature"
	r.AddCookie(cookies[0])

	_, err := cm.GetSigned(r, "token")
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got: %v", err)
	}
}

func TestCookieManager_MissingCookie(t *testing.T) {
	cm := NewCookieManager(testSecret)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := cm.GetSigned(r, "nonexistent")
	if err == nil {
		t.Error("expected error for missing cookie")
	}
}

func TestCookieManager_Clear(t *testing.T) {
	cm := NewCookieManager(testSecret)
	opts := CookieOptions{Path: "/"}

	w := httptest.NewRecorder()
	cm.Clear(w, "token", opts)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1", cookies[0].MaxAge)
	}
	if cookies[0].Value != "" {
		t.Errorf("Value = %q, want empty", cookies[0].Value)
	}
}

func TestCookieManager_DifferentSecrets(t *testing.T) {
	cm1 := NewCookieManager("secret-one-at-least-32-chars!!")
	cm2 := NewCookieManager("secret-two-at-least-32-chars!!")
	opts := CookieOptions{Path: "/"}

	w := httptest.NewRecorder()
	cm1.SetSigned(w, "token", "value", opts)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}

	_, err := cm2.GetSigned(r, "token")
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("different secrets should fail verification, got: %v", err)
	}
}

func TestCookieManager_SpecialCharacters(t *testing.T) {
	cm := NewCookieManager(testSecret)
	opts := CookieOptions{Path: "/"}

	// Value with special characters that might cause cookie issues
	value := `{"sub":"user@example.com","aud":"app-id"}`

	w := httptest.NewRecorder()
	cm.SetSigned(w, "data", value, opts)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}

	got, err := cm.GetSigned(r, "data")
	if err != nil {
		t.Fatalf("GetSigned() error: %v", err)
	}
	if got != value {
		t.Errorf("value = %q, want %q", got, value)
	}
}
