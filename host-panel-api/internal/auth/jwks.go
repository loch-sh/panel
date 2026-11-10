package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrTokenMalformed = errors.New("malformed JWT token")
	ErrTokenExpired   = errors.New("expired JWT token")
	ErrInvalidIssuer  = errors.New("invalid issuer (iss)")
	ErrInvalidAud     = errors.New("invalid audience (aud)")
	ErrKeyNotFound    = errors.New("JWK key not found for kid")
	ErrUnsupportedAlg = errors.New("unsupported signing algorithm")
)

// httpClient is a shared HTTP client with a strict timeout
// for all external calls (JWKS, OIDC discovery, token exchange).
var httpClient = &http.Client{Timeout: 10 * time.Second}

// JWKSVerifier fetches JWKS keys and verifies JWT tokens.
type JWKSVerifier struct {
	jwksURL  string
	issuer   string
	audience string

	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	lastFetched time.Time
	cacheTTL    time.Duration
}

// NewJWKSVerifier creates a JWT verifier based on a JWKS endpoint.
func NewJWKSVerifier(jwksURL, issuer, audience string) *JWKSVerifier {
	return &JWKSVerifier{
		jwksURL:  jwksURL,
		issuer:   issuer,
		audience: audience,
		keys:     make(map[string]*rsa.PublicKey),
		cacheTTL: 1 * time.Hour,
	}
}

// VerifyToken parses and verifies a JWT token.
// Returns the claims as a map, or an error.
func (v *JWKSVerifier) VerifyToken(ctx context.Context, tokenStr string) (map[string]any, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, ErrTokenMalformed
	}

	// Decode the header to get kid and alg
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decoding JWT header: %w", err)
	}

	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("parsing JWT header: %w", err)
	}

	if header.Alg != "RS256" {
		return nil, fmt.Errorf("%w : %s", ErrUnsupportedAlg, header.Alg)
	}

	// Find the public key
	key, err := v.getKey(ctx, header.Kid)
	if err != nil {
		return nil, err
	}

	// Verify the RS256 signature
	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decoding JWT signature: %w", err)
	}

	hash := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature); err != nil {
		return nil, fmt.Errorf("invalid JWT signature: %w", err)
	}

	// Decode the payload
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decoding JWT payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("parsing JWT payload: %w", err)
	}

	// Verify expiration (required)
	exp, ok := claims["exp"].(float64)
	if !ok {
		return nil, fmt.Errorf("%w: missing exp claim", ErrTokenMalformed)
	}
	if time.Now().Unix() > int64(exp) {
		return nil, ErrTokenExpired
	}

	// Verify the issuer
	if iss, ok := claims["iss"].(string); ok {
		if iss != v.issuer {
			return nil, fmt.Errorf("%w: expected %q, got %q", ErrInvalidIssuer, v.issuer, iss)
		}
	}

	// Verify the audience
	if err := v.verifyAudience(claims); err != nil {
		return nil, err
	}

	return claims, nil
}

// verifyAudience verifies the aud claim (can be string or []string).
func (v *JWKSVerifier) verifyAudience(claims map[string]any) error {
	aud, ok := claims["aud"]
	if !ok {
		return ErrInvalidAud
	}

	switch a := aud.(type) {
	case string:
		if a != v.audience {
			return fmt.Errorf("%w: expected %q, got %q", ErrInvalidAud, v.audience, a)
		}
	case []any:
		found := false
		for _, item := range a {
			if s, ok := item.(string); ok && s == v.audience {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: %q not found in audience", ErrInvalidAud, v.audience)
		}
	default:
		return ErrInvalidAud
	}

	return nil
}

// getKey returns the RSA key matching the given kid.
// Tries the cache first, then refreshes the keys if needed.
func (v *JWKSVerifier) getKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key, ok := v.keys[kid]
	expired := time.Since(v.lastFetched) > v.cacheTTL
	v.mu.RUnlock()

	if ok && !expired {
		return key, nil
	}

	// Refresh the keys
	if err := v.refreshKeys(ctx); err != nil {
		return nil, fmt.Errorf("refreshing JWKS: %w", err)
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	key, ok = v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("%w : %s", ErrKeyNotFound, kid)
	}
	return key, nil
}

// refreshKeys fetches keys from the JWKS endpoint.
func (v *JWKSVerifier) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("JWKS request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS response: status %d", resp.StatusCode)
	}

	var jwks struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decoding JWKS: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, raw := range jwks.Keys {
		var jwk struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		}
		if err := json.Unmarshal(raw, &jwk); err != nil {
			continue
		}
		if jwk.Kty != "RSA" {
			continue
		}

		key, err := parseRSAPublicKey(jwk.N, jwk.E)
		if err != nil {
			continue
		}
		newKeys[jwk.Kid] = key
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys = newKeys
	v.lastFetched = time.Now()
	return nil
}

// parseRSAPublicKey builds an RSA public key from the JWK n and e components.
func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decoding JWK n: %w", err)
	}

	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decoding JWK e: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	return &rsa.PublicKey{
		N: n,
		E: int(e.Int64()),
	}, nil
}
