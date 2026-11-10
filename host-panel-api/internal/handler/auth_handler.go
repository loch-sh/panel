package handler

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"host-panel-api/internal/auth"
	"host-panel-api/internal/config"
	"host-panel-api/internal/middleware"
)

// AuthHandler handles the OIDC authentication flow.
type AuthHandler struct {
	Provider auth.OIDCProvider
	Cookies  *auth.CookieManager
	Cfg      *config.Config
	Logger   *slog.Logger
	Audit    *slog.Logger
}

// authFlowData is stored in the signed "authFlow" cookie during the OIDC flow.
type authFlowData struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"`
}

// Login initiates the OIDC flow with PKCE.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	verifier, err := auth.GenerateCodeVerifier()
	if err != nil {
		h.Logger.Error("failed to generate code verifier", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal server error")
		return
	}

	state, err := auth.GenerateState()
	if err != nil {
		h.Logger.Error("failed to generate state", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal server error")
		return
	}

	challenge := auth.ComputeCodeChallenge(verifier)

	authURL, err := h.Provider.AuthorizationURL(r.Context(), h.Cfg.RedirectURI+"/openid-connect/redirect", challenge, state)
	if err != nil {
		h.Logger.Error("failed to build authorization URL", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal server error")
		return
	}

	// Store state + verifier in a signed cookie (5 minutes)
	flowData, _ := json.Marshal(authFlowData{
		State:    state,
		Verifier: verifier,
	})

	h.Cookies.SetSigned(w, "authFlow", string(flowData), auth.CookieOptions{
		Domain:   h.Cfg.CookieDomain,
		Path:     "/",
		MaxAge:   300, // 5 minutes
		Secure:   true,
		HTTPOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, authURL, http.StatusFound)
}

// Redirect handles the OAuth callback, exchanges the code and sets the JWT cookie.
func (h *AuthHandler) Redirect(w http.ResponseWriter, r *http.Request) {
	// Retrieve the authFlow cookie
	flowJSON, err := h.Cookies.GetSigned(r, "authFlow")
	if err != nil {
		h.Logger.Error("invalid authFlow cookie", "error", err)
		writeError(w, http.StatusBadRequest, ErrInvalidAuthFlow, "Invalid auth flow")
		return
	}

	var flow authFlowData
	if err := json.Unmarshal([]byte(flowJSON), &flow); err != nil {
		h.Logger.Error("failed to decode authFlow", "error", err)
		writeError(w, http.StatusBadRequest, ErrInvalidAuthFlow, "Invalid auth flow")
		return
	}

	// Verify the state
	if r.URL.Query().Get("state") != flow.State {
		h.Audit.Warn("login failed", "reason", "invalid state", "ip", middleware.ClientIP(r))
		writeError(w, http.StatusBadRequest, ErrInvalidState, "Invalid state parameter")
		return
	}

	// Check for provider error
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		h.Logger.Error("OIDC provider error",
			"error", errParam,
			"description", desc,
		)
		h.Audit.Warn("login failed", "reason", "provider error", "error", middleware.TruncateUTF8(errParam, 100), "ip", middleware.ClientIP(r))
		writeError(w, http.StatusForbidden, ErrAuthFailed, "Authentication failed")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, ErrMissingAuthCode, "Missing authorization code")
		return
	}

	// Exchange the code for tokens
	result, err := h.Provider.ExchangeCode(
		r.Context(),
		code,
		flow.Verifier,
		h.Cfg.RedirectURI+"/openid-connect/redirect",
	)
	if err != nil {
		h.Logger.Error("code exchange failed", "error", err)
		h.Audit.Warn("login failed", "reason", "token exchange failed", "ip", middleware.ClientIP(r))
		writeError(w, http.StatusForbidden, ErrTokenExchangeFailed, "Token exchange failed")
		return
	}

	// Audit login: extract email from the ID token payload (already trusted — exchanged with the provider)
	if email := extractEmailFromJWT(result.IDToken); email != "" {
		h.Audit.Info("user logged in", "user", email)
	} else {
		h.Audit.Warn("user logged in but email could not be extracted from ID token")
	}

	// Delete the authFlow cookie
	h.Cookies.Clear(w, "authFlow", auth.CookieOptions{
		Domain: h.Cfg.CookieDomain,
		Path:   "/",
	})

	// Store the JWT in a signed httpOnly cookie (1 hour)
	h.Cookies.SetSigned(w, "authorization", result.IDToken, auth.CookieOptions{
		Domain:   h.Cfg.CookieDomain,
		Path:     "/",
		MaxAge:   3600, // 1 hour
		Secure:   true,
		HTTPOnly: true,
		SameSite: http.SameSiteNoneMode,
	})

	http.Redirect(w, r, h.Cfg.FrontURL, http.StatusFound)
}

// Logout clears the cookies and redirects to the provider logout.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// The cookie is signed by this server: the token in it was issued by the
	// provider at login, so its email can be trusted for the audit entry.
	if token, err := h.Cookies.GetSigned(r, "authorization"); err == nil {
		if email := extractEmailFromJWT(token); email != "" {
			h.Audit.Info("user logged out", "user", email)
		}
	}
	cookieOpts := auth.CookieOptions{
		Domain: h.Cfg.CookieDomain,
		Path:   "/",
	}
	h.Cookies.Clear(w, "authFlow", cookieOpts)
	h.Cookies.Clear(w, "authorization", cookieOpts)

	logoutURL := h.Provider.LogoutURL()
	if logoutURL != "" {
		http.Redirect(w, r, logoutURL, http.StatusFound)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// extractEmailFromJWT decodes the payload of a JWT (without verification)
// and returns the email claim. Used for audit logging only — the token has
// already been exchanged with the OIDC provider and is trusted.
func extractEmailFromJWT(token string) string {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	if email, _ := claims["email"].(string); email != "" {
		return email
	}
	email, _ := claims["preferred_username"].(string)
	return email
}
