package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateCodeVerifier generates a PKCE code_verifier (32 random bytes, base64url).
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating code verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ComputeCodeChallenge computes the PKCE code_challenge from the verifier (S256).
func ComputeCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// GenerateState generates a random state for the OAuth flow (16 bytes, hex).
func GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating OAuth state: %w", err)
	}
	return hex.EncodeToString(b), nil
}
