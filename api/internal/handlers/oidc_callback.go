// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

const (
	oidcTimeout   = 10 * time.Second
	oidcCookieTTL = 600 // 10 minutes
)

// oidcConfigStore is the data-access surface for the OIDC callback handler.
type oidcConfigStore interface {
	GetSSOConfigByOrgSlug(ctx context.Context, slug string) (*types.OrgSSOConfig, error)
	GetOrg(ctx context.Context, orgID string) (*types.Organization, error)
	GetUserByEmail(ctx context.Context, email string) (*types.User, error)
	CreateUser(ctx context.Context, user *types.User) error
	AddOrgMember(ctx context.Context, orgID, userID string, role types.OrgRole, pendingKeyWrap bool) error
}

// OIDCCallbackHandler handles the OIDC authorization code flow with PKCE.
type OIDCCallbackHandler struct {
	store   oidcConfigStore
	baseURL string
}

// NewOIDCCallbackHandler constructs the handler.
func NewOIDCCallbackHandler(store oidcConfigStore, baseURL string) *OIDCCallbackHandler {
	return &OIDCCallbackHandler{store: store, baseURL: baseURL}
}

// Initiate handles GET /auth/oidc/:orgSlug/login — redirects to the OIDC
// provider with PKCE challenge and state CSRF token.
func (h *OIDCCallbackHandler) Initiate(c *gin.Context) {
	orgSlug := c.Param("orgSlug")

	cfg, err := h.store.GetSSOConfigByOrgSlug(c.Request.Context(), orgSlug)
	if err != nil || cfg == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SSO not configured for this org"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), oidcTimeout)
	defer cancel()

	provider, err := oidc.NewProvider(ctx, cfg.DiscoveryURL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach OIDC provider"})
		return
	}

	redirectURL := fmt.Sprintf("%s/auth/oidc/%s/callback", strings.TrimRight(h.baseURL, "/"), orgSlug)
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: string(decryptSSOSecret(cfg.EncryptedSecret)),
		Endpoint:     provider.Endpoint(),
		RedirectURL:  redirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}

	state, err := generateRandomString(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state"})
		return
	}

	// PKCE: generate code verifier and challenge.
	verifier, err := generateRandomString(48)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate PKCE verifier"})
		return
	}
	challenge := pkceChallenge(verifier)

	c.SetCookie("oidc_state", state, oidcCookieTTL, "/", "", false, true)
	c.SetCookie("oidc_verifier", verifier, oidcCookieTTL, "/", "", false, true)
	c.Redirect(http.StatusFound, oauthCfg.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	))
}

// Callback handles GET /auth/oidc/:orgSlug/callback.
func (h *OIDCCallbackHandler) Callback(c *gin.Context) {
	orgSlug := c.Param("orgSlug")
	code := c.Query("code")
	state := c.Query("state")

	if code == "" || state == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code or state"})
		return
	}

	storedState, err := c.Cookie("oidc_state")
	if err != nil || storedState != state {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid state"})
		return
	}
	verifier, err := c.Cookie("oidc_verifier")
	if err != nil || verifier == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing PKCE verifier"})
		return
	}
	c.SetCookie("oidc_state", "", -1, "/", "", false, true)
	c.SetCookie("oidc_verifier", "", -1, "/", "", false, true)

	cfg, err := h.store.GetSSOConfigByOrgSlug(c.Request.Context(), orgSlug)
	if err != nil || cfg == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SSO configuration not found"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), oidcTimeout)
	defer cancel()

	provider, err := oidc.NewProvider(ctx, cfg.DiscoveryURL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach OIDC provider"})
		return
	}

	redirectURL := fmt.Sprintf("%s/auth/oidc/%s/callback", strings.TrimRight(h.baseURL, "/"), orgSlug)
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: string(decryptSSOSecret(cfg.EncryptedSecret)),
		Endpoint:     provider.Endpoint(),
		RedirectURL:  redirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}

	token, err := oauthCfg.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", verifier),
	)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token exchange failed"})
		return
	}

	userInfo, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "failed to get user info"})
		return
	}

	var claims struct {
		Email  string   `json:"email"`
		Groups []string `json:"groups"`
	}
	if err := userInfo.Claims(&claims); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "failed to parse claims"})
		return
	}
	if claims.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "OIDC provider did not return an email"})
		return
	}

	// Determine role from group claims.
	role := types.OrgRoleMember
	for _, g := range claims.Groups {
		if g == cfg.GroupAdminClaim {
			role = types.OrgRoleAdmin
			break
		}
	}

	// Find or create user.
	user, err := h.store.GetUserByEmail(ctx, claims.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up user"})
		return
	}

	if user == nil {
		if !cfg.AutoProvision {
			c.JSON(http.StatusForbidden, gin.H{"error": "user not found and auto-provisioning is disabled"})
			return
		}
		user = &types.User{
			Email:    claims.Email,
			Username: claims.Email,
		}
		if err := h.store.CreateUser(ctx, user); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
			return
		}
	}

	// Ensure membership (log but don't fail — user may already be a member).
	pendingKeyWrap := role == types.OrgRoleAdmin
	if err := h.store.AddOrgMember(ctx, cfg.OrgID, user.ID, role, pendingKeyWrap); err != nil {
		// Not fatal: duplicate membership or concurrent provision.
		fmt.Fprintf(gin.DefaultErrorWriter, "oidc: add member failed for user %s org %s: %v\n", user.ID, cfg.OrgID, err)
	}

	// Redirect to the login page with a generic SSO flag. The actual session
	// issuance requires integration with the auth service (JWT signing), which
	// is a Phase 3b follow-up. For now, the user is provisioned and can log
	// in normally.
	c.Redirect(http.StatusFound, "/login?sso=ok")
}

func generateRandomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge computes the S256 code challenge from a verifier.
func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func decryptSSOSecret(encrypted []byte) []byte {
	return encrypted
}
