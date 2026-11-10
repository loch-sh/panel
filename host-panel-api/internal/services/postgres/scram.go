package postgres

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// scramIterations matches the PostgreSQL default (scram_iterations).
const scramIterations = 4096

// scramKeys derives the SCRAM-SHA-256 stored and server keys (RFC 5802).
// Passwords are generated ASCII, so SASLprep is the identity.
func scramKeys(password string, salt []byte, iterations int) (storedKey, serverKey []byte, err error) {
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return nil, nil, err
	}
	clientKey := hmacSHA256(salted, "Client Key")
	sum := sha256.Sum256(clientKey)
	return sum[:], hmacSHA256(salted, "Server Key"), nil
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

// scramVerifier returns the value PostgreSQL stores for a SCRAM-SHA-256
// password. Sending it instead of the password means the clear password
// never reaches the server, hence never its logs.
func scramVerifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	stored, server, err := scramKeys(password, salt, scramIterations)
	if err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", scramIterations, b64(salt), b64(stored), b64(server)), nil
}
