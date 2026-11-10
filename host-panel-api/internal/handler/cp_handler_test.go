package handler

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestValidateContainerPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid absolute path", "/var/log/app.log", false},
		{"valid root path", "/", false},
		{"valid deep path", "/usr/local/share/data/file.txt", false},
		{"empty path", "", true},
		{"relative path", "var/log/app.log", true},
		{"dot relative path", "./log/app.log", true},
		{"null byte", "/var/log/\x00app.log", true},
		{"newline", "/var/log/\napp.log", true},
		{"carriage return", "/var/log/\rapp.log", true},
		{"path too long", "/" + strings.Repeat("a", 4096), true},
		{"max length path", "/" + strings.Repeat("a", 4094), false},
		{"path with spaces", "/var/log/my app.log", false},
		{"path with dots", "/var/log/../etc/passwd", false}, // Clean() not applied here, just basic checks
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateContainerPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateContainerPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestClassifyCopyError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"missing directory", errors.New("Error response from daemon: no such file or directory"), http.StatusNotFound, ErrCopyDestNotFound},
		{"permission denied", errors.New("Error response from daemon: permission denied"), http.StatusForbidden, ErrCopyPermissionDenied},
		{"read-only fs", errors.New("read-only file system"), http.StatusForbidden, ErrCopyPermissionDenied},
		{"not a directory", errors.New("Error response from daemon: not a directory"), http.StatusBadRequest, ErrCopyDestNotDir},
		{"disk full", errors.New("no space left on device"), http.StatusInsufficientStorage, ErrCopyFailed},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, ErrCopyFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code, msg := classifyCopyError(tt.err, "/var/www")
			if status != tt.wantStatus || code != tt.wantCode {
				t.Errorf("classifyCopyError(%v) = (%d, %s), want (%d, %s)", tt.err, status, code, tt.wantStatus, tt.wantCode)
			}
			if msg == "" {
				t.Error("expected a non-empty message")
			}
		})
	}
}

func TestClassifySourceError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"missing path", errors.New("Error: No such container:path: c1:/nope: no such file or directory"), http.StatusNotFound, ErrCopyDestNotFound},
		{"daemon wording", errors.New("Could not find the file /nope in container c1"), http.StatusNotFound, ErrCopyDestNotFound},
		{"permission denied", errors.New("permission denied"), http.StatusForbidden, ErrCopyPermissionDenied},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, ErrCopyFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code, msg := classifySourceError(tt.err, "/var/www")
			if status != tt.wantStatus || code != tt.wantCode {
				t.Errorf("classifySourceError(%v) = (%d, %s), want (%d, %s)", tt.err, status, code, tt.wantStatus, tt.wantCode)
			}
			if msg == "" {
				t.Error("expected a non-empty message")
			}
		})
	}
}
