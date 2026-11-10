package services

import (
	"crypto/rand"
	"fmt"
)

// PasswordLength is the length of generated passwords (about 190 bits).
const PasswordLength = 32

const passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// GeneratePassword returns a random password made of [A-Za-z0-9] only: it
// needs no escaping in SQL literals, URIs or shells.
func GeneratePassword() (string, error) {
	// Rejection sampling: 248 is the largest multiple of 62 below 256, so
	// every character is equally likely.
	out := make([]byte, 0, PasswordLength)
	buf := make([]byte, 64)
	for len(out) < PasswordLength {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generating password: %w", err)
		}
		for _, b := range buf {
			if b < 248 && len(out) < PasswordLength {
				out = append(out, passwordAlphabet[int(b)%len(passwordAlphabet)])
			}
		}
	}
	return string(out), nil
}

// IsGeneratedPassword reports whether p has the exact shape of a generated
// password. Engines assert it right before building SQL.
func IsGeneratedPassword(p string) bool {
	if len(p) != PasswordLength {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
