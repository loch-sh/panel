package model

import (
	"fmt"
	"time"
)

const ScopeAll = "*"

// ValidScopes defines the set of allowed API key scopes.
var ValidScopes = map[string]bool{
	"*":                  true,
	"containers:read":    true,
	"containers:logs":    true,
	"containers:actions": true,
	"containers:shell":   true,
	"containers:proxy":   true,
	"containers:stats":   true,
	"containers:cp":      true,
	"cron":               true,
	"sftp:read":          true,
	"sftp:write":         true,
	"hosting:read":       true,
	"hosting:write":      true,
	"services:read":      true,
	"services:write":     true,
	"services:expose":    true,
	"networks:read":      true,
	"networks:write":     true,
	"functions:read":     true,
	"functions:write":    true,
	"functions:deploy":   true,
	"functions:invoke":   true,
	"audit:read":         true,
}

// APIKey represents an API key for CLI authentication.
type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"keyHash"`
	KeyPrefix  string     `json:"keyPrefix"`
	UserEmail  string     `json:"userEmail"`
	Scopes     []string   `json:"scopes,omitempty"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// IsExpired returns true if the key has passed its expiration date.
func (k *APIKey) IsExpired() bool {
	return time.Now().After(k.ExpiresAt)
}

// HasScope returns true if the key grants the given scope.
// Legacy keys (empty Scopes) have full access for backward compatibility.
func (k *APIKey) HasScope(scope string) bool {
	if len(k.Scopes) == 0 {
		return true
	}
	for _, s := range k.Scopes {
		if s == ScopeAll || s == scope {
			return true
		}
	}
	return false
}

// ValidateScopes checks that all provided scopes are valid.
func ValidateScopes(scopes []string) error {
	if len(scopes) == 0 {
		return fmt.Errorf("at least one scope is required")
	}
	for _, s := range scopes {
		if !ValidScopes[s] {
			return fmt.Errorf("invalid scope: %s", s)
		}
	}
	if len(scopes) > 1 {
		for _, s := range scopes {
			if s == ScopeAll {
				return fmt.Errorf("wildcard scope '*' cannot be combined with other scopes")
			}
		}
	}
	return nil
}
