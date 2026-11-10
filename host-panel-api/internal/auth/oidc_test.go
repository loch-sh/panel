package auth

import (
	"encoding/base64"
	"testing"
)

func TestGenerateCodeVerifier(t *testing.T) {
	v, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier() error: %v", err)
	}

	// 32 random bytes → 43 base64url characters (no padding)
	if len(v) != 43 {
		t.Errorf("verifier length = %d, want 43", len(v))
	}

	// Must be valid base64url
	if _, err := base64.RawURLEncoding.DecodeString(v); err != nil {
		t.Errorf("verifier is not valid base64url: %v", err)
	}

	// Two calls should produce different values
	v2, _ := GenerateCodeVerifier()
	if v == v2 {
		t.Error("two consecutive calls should produce different verifiers")
	}
}

func TestComputeCodeChallenge(t *testing.T) {
	// Deterministic: same input → same output
	challenge1 := ComputeCodeChallenge("test-verifier")
	challenge2 := ComputeCodeChallenge("test-verifier")
	if challenge1 != challenge2 {
		t.Error("same verifier should produce the same challenge")
	}

	// Different input → different output
	challenge3 := ComputeCodeChallenge("other-verifier")
	if challenge1 == challenge3 {
		t.Error("different verifiers should produce different challenges")
	}

	// Output should be valid base64url
	if _, err := base64.RawURLEncoding.DecodeString(challenge1); err != nil {
		t.Errorf("challenge is not valid base64url: %v", err)
	}

	// SHA-256 output is 32 bytes → 43 base64url characters
	if len(challenge1) != 43 {
		t.Errorf("challenge length = %d, want 43", len(challenge1))
	}
}

func TestGenerateState(t *testing.T) {
	state, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState() error: %v", err)
	}

	// 16 random bytes → 32 hex characters
	if len(state) != 32 {
		t.Errorf("state length = %d, want 32", len(state))
	}

	// Must be valid hex (only 0-9, a-f)
	for _, c := range state {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("state contains invalid hex character: %c", c)
			break
		}
	}

	// Two calls should produce different values
	state2, _ := GenerateState()
	if state == state2 {
		t.Error("two consecutive calls should produce different states")
	}
}
