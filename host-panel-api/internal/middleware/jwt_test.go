package middleware

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"host-panel-api/internal/auth"
)

// ─── Test helpers ───────────────────────────────────────────────────────────

const (
	testIssuer   = "https://test-issuer.example.com"
	testAudience = "test-audience-id"
	testKid      = "test-key-1"
	testSecret   = "this-is-a-test-cookie-secret-32-chars!"
)

// testRSAKey holds an RSA key pair for tests.
var testRSAKey *rsa.PrivateKey

func init() {
	var err error
	testRSAKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("failed to generate test RSA key: " + err.Error())
	}
}

// startJWKSServer starts a mock HTTP server that serves JWKS keys.
func startJWKSServer(t *testing.T) *httptest.Server {
	t.Helper()

	pub := testRSAKey.PublicKey
	nB64 := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	eB64 := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())

	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"kid": testKid,
				"n":   nB64,
				"e":   eB64,
				"use": "sig",
				"alg": "RS256",
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// makeJWT builds an RS256-signed JWT with the given claims.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	return makeJWTWithAlg(t, "RS256", testKid, claims, true)
}

// makeJWTWithAlg builds a JWT with a specific algorithm and kid.
func makeJWTWithAlg(t *testing.T, alg, kid string, claims map[string]any, sign bool) string {
	t.Helper()

	header := map[string]string{"alg": alg, "typ": "JWT", "kid": kid}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	claimsJSON, _ := json.Marshal(claims)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64

	if !sign {
		return signingInput + "."
	}

	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, testRSAKey, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("JWT signing error: %v", err)
	}
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64
}

// validClaims returns valid JWT claims for tests.
func validClaims() map[string]any {
	return map[string]any{
		"iss":   testIssuer,
		"aud":   testAudience,
		"exp":   float64(time.Now().Add(time.Hour).Unix()),
		"email": "test@example.com",
		"name":  "Test User",
	}
}

// newJWTMiddleware creates a JWT middleware with the mock JWKS server.
func newJWTMiddleware(t *testing.T) func(http.Handler) http.Handler {
	t.Helper()
	srv := startJWKSServer(t)
	verifier := auth.NewJWKSVerifier(srv.URL, testIssuer, testAudience)
	cookies := auth.NewCookieManager(testSecret)
	return JWT(verifier, cookies)
}

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestJWT_ValidBearerToken(t *testing.T) {
	mw := newJWTMiddleware(t)
	token := makeJWT(t, validClaims())

	var gotClaims map[string]any
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := ClaimsFromCtx(r.Context())
		if ok {
			gotClaims = c
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200", rr.Code)
	}
	if gotClaims == nil {
		t.Fatal("claims must be present in context")
	}
	if email, _ := gotClaims["email"].(string); email != "test@example.com" {
		t.Errorf("email = %q, expected %q", email, "test@example.com")
	}
}

func TestJWT_ValidCookieToken(t *testing.T) {
	srv := startJWKSServer(t)
	verifier := auth.NewJWKSVerifier(srv.URL, testIssuer, testAudience)
	cookies := auth.NewCookieManager(testSecret)
	mw := JWT(verifier, cookies)

	token := makeJWT(t, validClaims())

	// Create a signed cookie with the CookieManager
	cookieRR := httptest.NewRecorder()
	cookies.SetSigned(cookieRR, "authorization", token, auth.CookieOptions{
		Path:     "/",
		HTTPOnly: true,
	})
	setCookieHeader := cookieRR.Header().Get("Set-Cookie")

	var gotClaims map[string]any
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := ClaimsFromCtx(r.Context())
		if ok {
			gotClaims = c
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", setCookieHeader)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200", rr.Code)
	}
	if gotClaims == nil {
		t.Fatal("claims must be present via the signed cookie")
	}
}

func TestJWT_BearerPriorityOverCookie(t *testing.T) {
	srv := startJWKSServer(t)
	verifier := auth.NewJWKSVerifier(srv.URL, testIssuer, testAudience)
	cookies := auth.NewCookieManager(testSecret)
	mw := JWT(verifier, cookies)

	bearerClaims := validClaims()
	bearerClaims["email"] = "bearer@example.com"
	bearerToken := makeJWT(t, bearerClaims)

	cookieClaims := validClaims()
	cookieClaims["email"] = "cookie@example.com"
	cookieToken := makeJWT(t, cookieClaims)

	cookieRR := httptest.NewRecorder()
	cookies.SetSigned(cookieRR, "authorization", cookieToken, auth.CookieOptions{Path: "/"})
	setCookieHeader := cookieRR.Header().Get("Set-Cookie")

	var gotEmail string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := ClaimsFromCtx(r.Context())
		gotEmail, _ = c["email"].(string)
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Cookie", setCookieHeader)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200", rr.Code)
	}
	if gotEmail != "bearer@example.com" {
		t.Errorf("email = %q — Bearer must take priority over cookie", gotEmail)
	}
}

func TestJWT_MissingToken(t *testing.T) {
	mw := newJWTMiddleware(t)
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("handler must not be called without a token")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestJWT_MalformedToken(t *testing.T) {
	mw := newJWTMiddleware(t)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be called with a malformed token")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-jwt")
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusForbidden)
	}
}

func TestJWT_ExpiredToken(t *testing.T) {
	mw := newJWTMiddleware(t)
	claims := validClaims()
	claims["exp"] = float64(time.Now().Add(-time.Hour).Unix()) // expired 1h ago
	token := makeJWT(t, claims)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be called with an expired token")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusForbidden)
	}
}

func TestJWT_WrongIssuer(t *testing.T) {
	mw := newJWTMiddleware(t)
	claims := validClaims()
	claims["iss"] = "https://evil-issuer.example.com"
	token := makeJWT(t, claims)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be called with a wrong issuer")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusForbidden)
	}
}

func TestJWT_WrongAudience(t *testing.T) {
	mw := newJWTMiddleware(t)
	claims := validClaims()
	claims["aud"] = "wrong-audience"
	token := makeJWT(t, claims)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be called with a wrong audience")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusForbidden)
	}
}

func TestJWT_AlgNone(t *testing.T) {
	mw := newJWTMiddleware(t)
	// alg:none attack attempt — the token has no signature
	token := makeJWTWithAlg(t, "none", testKid, validClaims(), false)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must NEVER be called with alg=none")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("alg:none attack: status = %d, expected %d", rr.Code, http.StatusForbidden)
	}
}

func TestJWT_InvalidSignature(t *testing.T) {
	mw := newJWTMiddleware(t)
	token := makeJWT(t, validClaims())

	// Alter a character in the middle of the signature (not at the end,
	// because base64 padding bits would be ignored during decoding)
	lastDot := strings.LastIndex(token, ".")
	sigMid := lastDot + 1 + (len(token)-lastDot-1)/2
	c := byte('A')
	if token[sigMid] == 'A' {
		c = 'B'
	}
	tampered := token[:sigMid] + string(c) + token[sigMid+1:]

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be called with an invalid signature")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("invalid signature: status = %d, expected %d", rr.Code, http.StatusForbidden)
	}
}

func TestJWT_TamperedCookie(t *testing.T) {
	srv := startJWKSServer(t)
	verifier := auth.NewJWKSVerifier(srv.URL, testIssuer, testAudience)
	cookies := auth.NewCookieManager(testSecret)
	mw := JWT(verifier, cookies)

	token := makeJWT(t, validClaims())

	// Create a signed cookie then tamper with the value (not the Set-Cookie attributes)
	cookieRR := httptest.NewRecorder()
	cookies.SetSigned(cookieRR, "authorization", token, auth.CookieOptions{Path: "/"})

	// Extract the raw cookie and tamper with the HMAC signature
	rawCookie := cookieRR.Result().Cookies()[0]
	// The format is "<base64url-value>.<base64url-hmac>" — we alter the last chars of the HMAC
	val := rawCookie.Value
	tampered := val[:len(val)-4] + "XXXX"

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be called with a tampered cookie")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "authorization", Value: tampered})
	handler.ServeHTTP(rr, req)

	// Tampered cookie -> GetSigned fails (invalid signature) -> no token -> 401
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("tampered cookie: status = %d, expected %d", rr.Code, http.StatusUnauthorized)
	}
}
