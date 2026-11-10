package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"host-panel-api/internal/auth"
	"host-panel-api/internal/config"
)

// mockOIDCProvider implements auth.OIDCProvider for testing.
type mockOIDCProvider struct {
	authURL       string
	authURLErr    error
	exchangeToken *auth.TokenResult
	exchangeErr   error
	logoutURL     string
}

func (m *mockOIDCProvider) Name() string { return "mock" }
func (m *mockOIDCProvider) AuthorizationURL(_ context.Context, redirectURI, codeChallenge, state string) (string, error) {
	if m.authURLErr != nil {
		return "", m.authURLErr
	}
	if m.authURL != "" {
		return m.authURL, nil
	}
	return fmt.Sprintf("https://idp.example.com/authorize?redirect_uri=%s&state=%s", redirectURI, state), nil
}
func (m *mockOIDCProvider) ExchangeCode(_ context.Context, code, codeVerifier, redirectURI string) (*auth.TokenResult, error) {
	if m.exchangeErr != nil {
		return nil, m.exchangeErr
	}
	return m.exchangeToken, nil
}
func (m *mockOIDCProvider) LogoutURL() string { return m.logoutURL }
func (m *mockOIDCProvider) JWKSURL() string   { return "https://idp.example.com/.well-known/jwks" }
func (m *mockOIDCProvider) Issuer() string    { return "https://idp.example.com" }
func (m *mockOIDCProvider) Audience() string  { return "test-client-id" }

func newTestAuthHandler(provider *mockOIDCProvider) *AuthHandler {
	return &AuthHandler{
		Provider: provider,
		Cookies:  auth.NewCookieManager("test-cookie-secret-at-least-32-chars!!"),
		Cfg: &config.Config{
			FrontURL:     "http://localhost:5173",
			RedirectURI:  "http://localhost:2538",
			CookieDomain: "",
		},
		Logger: testLogger(),
		Audit:  testLogger(),
	}
}

func TestAuthHandler_Login_Redirects(t *testing.T) {
	provider := &mockOIDCProvider{}
	h := newTestAuthHandler(provider)

	r := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	w := httptest.NewRecorder()

	h.Login(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}

	location := w.Header().Get("Location")
	if !strings.Contains(location, "idp.example.com/authorize") {
		t.Errorf("Location = %q, expected IdP authorize URL", location)
	}

	// authFlow cookie should be set
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "authFlow" {
			found = true
		}
	}
	if !found {
		t.Error("authFlow cookie should be set")
	}
}

func TestAuthHandler_Redirect_Success(t *testing.T) {
	// Create a fake JWT with email claim
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"user@test.com","sub":"123"}`))
	fakeJWT := header + "." + payload + ".fake-sig"

	provider := &mockOIDCProvider{
		exchangeToken: &auth.TokenResult{
			IDToken:     fakeJWT,
			AccessToken: "access-token",
			ExpiresIn:   3600,
		},
	}
	h := newTestAuthHandler(provider)

	// First, simulate a login to set the authFlow cookie
	loginReq := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	loginW := httptest.NewRecorder()
	h.Login(loginW, loginReq)

	// Get the authFlow cookie and extract the state
	var authFlowCookie *http.Cookie
	for _, c := range loginW.Result().Cookies() {
		if c.Name == "authFlow" {
			authFlowCookie = c
		}
	}
	if authFlowCookie == nil {
		t.Fatal("authFlow cookie not found")
	}

	// Decode the flow data to get the state
	flowJSON, err := h.Cookies.GetSigned(
		func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(authFlowCookie)
			return r
		}(),
		"authFlow",
	)
	if err != nil {
		t.Fatalf("GetSigned: %v", err)
	}
	var flow authFlowData
	json.Unmarshal([]byte(flowJSON), &flow)

	// Now call redirect with the state and code
	redirectURL := fmt.Sprintf("/auth/redirect?state=%s&code=test-code", flow.State)
	r := httptest.NewRequest(http.MethodGet, redirectURL, nil)
	r.AddCookie(authFlowCookie)
	w := httptest.NewRecorder()

	h.Redirect(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}

	location := w.Header().Get("Location")
	if location != "http://localhost:5173" {
		t.Errorf("Location = %q, want %q", location, "http://localhost:5173")
	}

	// authorization cookie should be set
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "authorization" {
			found = true
		}
	}
	if !found {
		t.Error("authorization cookie should be set")
	}
}

func TestAuthHandler_Redirect_InvalidState(t *testing.T) {
	provider := &mockOIDCProvider{}
	h := newTestAuthHandler(provider)

	// Set up a valid authFlow cookie
	loginReq := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	loginW := httptest.NewRecorder()
	h.Login(loginW, loginReq)

	var authFlowCookie *http.Cookie
	for _, c := range loginW.Result().Cookies() {
		if c.Name == "authFlow" {
			authFlowCookie = c
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/auth/redirect?state=wrong-state&code=test-code", nil)
	r.AddCookie(authFlowCookie)
	w := httptest.NewRecorder()

	h.Redirect(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAuthHandler_Redirect_MissingCode(t *testing.T) {
	provider := &mockOIDCProvider{}
	h := newTestAuthHandler(provider)

	loginReq := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	loginW := httptest.NewRecorder()
	h.Login(loginW, loginReq)

	var authFlowCookie *http.Cookie
	for _, c := range loginW.Result().Cookies() {
		if c.Name == "authFlow" {
			authFlowCookie = c
		}
	}

	flowJSON, _ := h.Cookies.GetSigned(
		func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(authFlowCookie)
			return r
		}(),
		"authFlow",
	)
	var flow authFlowData
	json.Unmarshal([]byte(flowJSON), &flow)

	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/auth/redirect?state=%s", flow.State), nil)
	r.AddCookie(authFlowCookie)
	w := httptest.NewRecorder()

	h.Redirect(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAuthHandler_Redirect_ProviderError(t *testing.T) {
	provider := &mockOIDCProvider{}
	h := newTestAuthHandler(provider)

	loginReq := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	loginW := httptest.NewRecorder()
	h.Login(loginW, loginReq)

	var authFlowCookie *http.Cookie
	for _, c := range loginW.Result().Cookies() {
		if c.Name == "authFlow" {
			authFlowCookie = c
		}
	}

	flowJSON, _ := h.Cookies.GetSigned(
		func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(authFlowCookie)
			return r
		}(),
		"authFlow",
	)
	var flow authFlowData
	json.Unmarshal([]byte(flowJSON), &flow)

	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/auth/redirect?state=%s&error=access_denied&error_description=user+denied", flow.State), nil)
	r.AddCookie(authFlowCookie)
	w := httptest.NewRecorder()

	h.Redirect(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestAuthHandler_Redirect_MissingAuthFlowCookie(t *testing.T) {
	provider := &mockOIDCProvider{}
	h := newTestAuthHandler(provider)

	r := httptest.NewRequest(http.MethodGet, "/auth/redirect?state=x&code=y", nil)
	w := httptest.NewRecorder()

	h.Redirect(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAuthHandler_Logout_WithLogoutURL(t *testing.T) {
	provider := &mockOIDCProvider{logoutURL: "https://idp.example.com/logout"}
	h := newTestAuthHandler(provider)

	r := httptest.NewRequest(http.MethodGet, "/auth/logout", nil)
	w := httptest.NewRecorder()

	h.Logout(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}

	location := w.Header().Get("Location")
	if location != "https://idp.example.com/logout" {
		t.Errorf("Location = %q, want IdP logout URL", location)
	}
}

func TestAuthHandler_Logout_WithoutLogoutURL(t *testing.T) {
	provider := &mockOIDCProvider{logoutURL: ""}
	h := newTestAuthHandler(provider)

	r := httptest.NewRequest(http.MethodGet, "/auth/logout", nil)
	w := httptest.NewRecorder()

	h.Logout(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]string
	json.NewDecoder(w.Result().Body).Decode(&body)
	if body["message"] != "logged out" {
		t.Errorf("message = %q, want %q", body["message"], "logged out")
	}
}

func TestExtractEmailFromJWT(t *testing.T) {
	tests := []struct {
		name     string
		claims   map[string]any
		wantMail string
	}{
		{
			"email claim",
			map[string]any{"email": "user@test.com", "sub": "123"},
			"user@test.com",
		},
		{
			"preferred_username fallback",
			map[string]any{"preferred_username": "user@test.com", "sub": "123"},
			"user@test.com",
		},
		{
			"no email",
			map[string]any{"sub": "123"},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
			payloadJSON, _ := json.Marshal(tt.claims)
			payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
			token := header + "." + payload + ".fake-sig"

			got := extractEmailFromJWT(token)
			if got != tt.wantMail {
				t.Errorf("extractEmailFromJWT() = %q, want %q", got, tt.wantMail)
			}
		})
	}
}

func TestExtractEmailFromJWT_MalformedToken(t *testing.T) {
	if email := extractEmailFromJWT("not-a-jwt"); email != "" {
		t.Errorf("expected empty for malformed token, got %q", email)
	}
	if email := extractEmailFromJWT("a.b"); email != "" {
		t.Errorf("expected empty for 2-part token, got %q", email)
	}
}
