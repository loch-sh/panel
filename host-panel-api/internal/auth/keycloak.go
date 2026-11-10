package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"host-panel-api/internal/config"
)

// KeycloakProvider implements OIDCProvider for Keycloak.
type KeycloakProvider struct {
	clientID     string
	clientSecret string
	scope        string

	// Endpoints discovered via .well-known/openid-configuration
	authEndpoint  string
	tokenEndpoint string
	jwksURI       string
	logoutURI     string
	issuer        string
}

// NewKeycloakProvider creates a Keycloak OIDC provider by automatically
// discovering endpoints via .well-known/openid-configuration.
func NewKeycloakProvider(ctx context.Context, cfg *config.Config) (*KeycloakProvider, error) {
	discoveryURL := fmt.Sprintf(
		"%s/realms/%s/.well-known/openid-configuration",
		strings.TrimRight(cfg.KeycloakURL, "/"),
		cfg.KeycloakRealm,
	)

	disc, err := fetchDiscovery(ctx, discoveryURL)
	if err != nil {
		return nil, err
	}

	return &KeycloakProvider{
		clientID:      cfg.ClientID,
		clientSecret:  cfg.ClientSecret,
		scope:         cfg.Scope,
		authEndpoint:  disc.AuthorizationEndpoint,
		tokenEndpoint: disc.TokenEndpoint,
		jwksURI:       disc.JWKSURI,
		logoutURI:     disc.EndSessionEndpoint,
		issuer:        disc.Issuer,
	}, nil
}

func (p *KeycloakProvider) Name() string {
	return "Keycloak"
}

func (p *KeycloakProvider) AuthorizationURL(_ context.Context, redirectURI, codeChallenge, state string) (string, error) {
	params := url.Values{
		"client_id":             {p.clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {p.scope},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return p.authEndpoint + "?" + params.Encode(), nil
}

func (p *KeycloakProvider) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*TokenResult, error) {
	data := url.Values{
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
		"code":          {code},
		"code_verifier": {codeVerifier},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
		"scope":         {p.scope},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("token exchange failed (status %d): %s — %s",
			resp.StatusCode, errResp.Error, errResp.Description)
	}

	var tokenResp struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}

	return &TokenResult{
		IDToken:     tokenResp.IDToken,
		AccessToken: tokenResp.AccessToken,
		ExpiresIn:   tokenResp.ExpiresIn,
	}, nil
}

func (p *KeycloakProvider) LogoutURL() string {
	return p.logoutURI
}

func (p *KeycloakProvider) JWKSURL() string {
	return p.jwksURI
}

func (p *KeycloakProvider) Issuer() string {
	return p.issuer
}

func (p *KeycloakProvider) Audience() string {
	return p.clientID
}
