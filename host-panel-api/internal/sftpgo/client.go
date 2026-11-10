// Package sftpgo is a thin client for the SFTPGo admin REST API (v2).
// It is used by the host panel to manage SFTP users in "web agency" mode.
// SFTPGo is the single source of truth: the host panel keeps no local copy.
package sftpgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiKeyHeader is the header SFTPGo uses for direct API key authentication
// (securityScheme "APIKeyAuth"). The value is the admin API key.
const apiKeyHeader = "X-SFTPGO-API-KEY"

// Client talks to the SFTPGo admin REST API.
type Client struct {
	baseURL    string // e.g. http://sftpgo:8080 (no trailing slash)
	apiKey     string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient builds a client for the SFTPGo admin API.
//
// It deliberately uses a plain http.Client. Unlike the cron executor, we must
// NOT use an SSRF-blocking transport here: the SFTPGo admin API lives on a
// private/internal host that such a transport would reject.
func NewClient(baseURL, apiKey string, logger *slog.Logger) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     logger,
	}
}

// ListUsers returns all SFTPGo users.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var users []User
	if err := c.do(ctx, http.MethodGet, "/api/v2/users", nil, &users); err != nil {
		return nil, err
	}
	return users, nil
}

// GetUser returns a single user by username.
func (c *Client) GetUser(ctx context.Context, username string) (*User, error) {
	var user User
	if err := c.do(ctx, http.MethodGet, "/api/v2/users/"+url.PathEscape(username), nil, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateUser creates a new user.
func (c *Client) CreateUser(ctx context.Context, user *User) (*User, error) {
	var created User
	if err := c.do(ctx, http.MethodPost, "/api/v2/users", user, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

// UpdateUser replaces an existing user.
func (c *Client) UpdateUser(ctx context.Context, username string, user *User) error {
	return c.do(ctx, http.MethodPut, "/api/v2/users/"+url.PathEscape(username), user, nil)
}

// DeleteUser removes a user by username.
func (c *Client) DeleteUser(ctx context.Context, username string) error {
	return c.do(ctx, http.MethodDelete, "/api/v2/users/"+url.PathEscape(username), nil, nil)
}

// UserToken authenticates as a regular user (Basic Auth) and returns a short
// lived access token for the user-scoped APIs. SFTPGo has no admin endpoint to
// manage another user's filesystem, so operating on a user's files requires
// impersonating them with their own credentials.
func (c *Client) UserToken(ctx context.Context, username, password string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v2/user/token", nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(username, password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling sftpgo: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return "", fmt.Errorf("decoding sftpgo token: %w", err)
	}
	if token.AccessToken == "" {
		return "", errors.New("sftpgo returned an empty access token")
	}
	return token.AccessToken, nil
}

// CreateUserDir creates a directory (with its parents) in the filesystem of the
// user owning the given access token. dirPath is relative to the user's home.
func (c *Client) CreateUserDir(ctx context.Context, token, dirPath string) error {
	q := url.Values{}
	q.Set("path", dirPath)
	q.Set("mkdir_parents", "true")
	endpoint := c.baseURL + "/api/v2/user/dirs?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling sftpgo: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	return nil
}

// APIError is returned when SFTPGo responds with a non-2xx status.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("sftpgo API error: status %d: %s", e.StatusCode, e.Body)
}

// NotFound reports whether the error is an SFTPGo 404.
func NotFound(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// do performs an authenticated request and, when out != nil, decodes the JSON body.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(apiKeyHeader, c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling sftpgo: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding sftpgo response: %w", err)
		}
	}
	return nil
}
