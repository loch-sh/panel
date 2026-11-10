package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// TokenResult holds the tokens received after the authorization code exchange.
type TokenResult struct {
	IDToken     string
	AccessToken string
	ExpiresIn   int
}

// OIDCProvider defines the contract for an OpenID Connect provider.
// The interface is designed to be interchangeable (Azure, Keycloak, etc.).
type OIDCProvider interface {
	// Name returns the provider name for logging.
	Name() string

	// AuthorizationURL builds the authorization URL for the login redirect.
	AuthorizationURL(ctx context.Context, redirectURI, codeChallenge, state string) (string, error)

	// ExchangeCode exchanges an authorization code for tokens.
	ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*TokenResult, error)

	// LogoutURL returns the provider's logout URL.
	LogoutURL() string

	// JWKSURL returns the JSON Web Key Set URL.
	JWKSURL() string

	// Issuer returns the expected value of the "iss" claim in JWTs.
	Issuer() string

	// Audience returns the expected value of the "aud" claim in JWTs.
	Audience() string
}

// oidcDiscovery represents the .well-known/openid-configuration response.
type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
	Issuer                string `json:"issuer"`
}

// fetchDiscovery fetches and decodes the OIDC discovery configuration.
func fetchDiscovery(ctx context.Context, discoveryURL string) (*oidcDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating OIDC discovery request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery: status %d", resp.StatusCode)
	}

	var disc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return nil, fmt.Errorf("decoding OIDC discovery: %w", err)
	}

	return &disc, nil
}
