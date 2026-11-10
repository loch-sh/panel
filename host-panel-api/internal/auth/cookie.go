package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrInvalidSignature = errors.New("invalid cookie signature")

// CookieOptions configures the attributes of an HTTP cookie.
type CookieOptions struct {
	Domain   string
	Path     string
	MaxAge   int
	Secure   bool
	HTTPOnly bool
	SameSite http.SameSite
}

// CookieManager handles cookie signing and verification.
type CookieManager struct {
	secret string
}

// NewCookieManager creates a signed cookie manager.
func NewCookieManager(secret string) *CookieManager {
	return &CookieManager{secret: secret}
}

// SetSigned writes an HMAC-SHA256 signed cookie to the response.
func (m *CookieManager) SetSigned(w http.ResponseWriter, name, value string, opts CookieOptions) {
	signed := signValue(value, m.secret)
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    signed,
		Domain:   opts.Domain,
		Path:     opts.Path,
		MaxAge:   opts.MaxAge,
		Secure:   opts.Secure,
		HttpOnly: opts.HTTPOnly,
		SameSite: opts.SameSite,
	})
}

// GetSigned reads and verifies a signed cookie. Returns the original value.
func (m *CookieManager) GetSigned(r *http.Request, name string) (string, error) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return "", err
	}
	return unsignValue(cookie.Value, m.secret)
}

// Clear deletes a cookie by setting MaxAge=-1.
func (m *CookieManager) Clear(w http.ResponseWriter, name string, opts CookieOptions) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Domain:   opts.Domain,
		Path:     opts.Path,
		MaxAge:   -1,
		Secure:   opts.Secure,
		HttpOnly: opts.HTTPOnly,
		SameSite: opts.SameSite,
	})
}

// signValue encodes the value in base64url then signs the whole thing.
// Final format: "<base64url-value>.<base64url-hmac>"
// All characters are RFC 6265 compatible (no ", {, }, spaces).
func signValue(value, secret string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(value))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig
}

// unsignValue verifies the signature and decodes the original value.
// Uses hmac.Equal for constant-time comparison.
func unsignValue(signed, secret string) (string, error) {
	lastDot := strings.LastIndex(signed, ".")
	if lastDot < 0 {
		return "", ErrInvalidSignature
	}

	encoded := signed[:lastDot]
	providedSig := signed[lastDot+1:]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(providedSig), []byte(expectedSig)) {
		return "", ErrInvalidSignature
	}

	value, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decoding cookie value: %w", err)
	}

	return string(value), nil
}
