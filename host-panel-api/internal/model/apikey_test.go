package model

import (
	"testing"
	"time"
)

func TestAPIKey_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"expired yesterday", time.Now().Add(-24 * time.Hour), true},
		{"expired one second ago", time.Now().Add(-1 * time.Second), true},
		{"expires in one hour", time.Now().Add(1 * time.Hour), false},
		{"expires in one year", time.Now().Add(365 * 24 * time.Hour), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := &APIKey{ExpiresAt: tt.expiresAt}
			if got := key.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIKey_HasScope(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		scope  string
		want   bool
	}{
		{"legacy empty scopes has full access", nil, "containers:read", true},
		{"wildcard grants any scope", []string{"*"}, "containers:read", true},
		{"exact match", []string{"containers:read", "containers:logs"}, "containers:read", true},
		{"no match", []string{"containers:read"}, "containers:actions", false},
		{"single scope match", []string{"cron"}, "cron", true},
		{"single scope no match", []string{"cron"}, "containers:read", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := &APIKey{Scopes: tt.scopes}
			if got := key.HasScope(tt.scope); got != tt.want {
				t.Errorf("HasScope(%q) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

func TestValidateScopes(t *testing.T) {
	tests := []struct {
		name    string
		scopes  []string
		wantErr bool
	}{
		{"valid single scope", []string{"containers:read"}, false},
		{"valid multiple scopes", []string{"containers:read", "containers:logs"}, false},
		{"valid wildcard alone", []string{"*"}, false},
		{"valid cron scope", []string{"cron"}, false},
		{"all valid scopes", []string{"containers:read", "containers:logs", "containers:actions", "containers:shell", "containers:proxy", "containers:stats", "cron"}, false},
		{"empty scopes", nil, true},
		{"invalid scope", []string{"invalid"}, true},
		{"mixed valid and invalid", []string{"containers:read", "invalid"}, true},
		{"wildcard combined with other", []string{"*", "containers:read"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateScopes(tt.scopes)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateScopes(%v) error = %v, wantErr %v", tt.scopes, err, tt.wantErr)
			}
		})
	}
}
