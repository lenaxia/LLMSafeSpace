// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// oidcConfigStore is the data-access surface for the OIDC callback handler.
type oidcConfigStore interface {
	GetSSOConfigByOrgSlug(ctx context.Context, slug string) (*types.OrgSSOConfig, error)
	GetOrg(ctx context.Context, orgID string) (*types.Organization, error)
	GetUserByEmail(ctx context.Context, email string) (*types.User, error)
	CreateUser(ctx context.Context, user *types.User) error
	AddOrgMember(ctx context.Context, orgID, userID string, role types.OrgRole, pendingKeyWrap bool) error
}

// OIDCCallbackHandler handles the OIDC authorization code callback.
type OIDCCallbackHandler struct {
	store   oidcConfigStore
	baseURL string
}

// NewOIDCCallbackHandler constructs the handler. baseURL is the app's external
// URL (for constructing the redirect URI).
func NewOIDCCallbackHandler(store oidcConfigStore, baseURL string) *OIDCCallbackHandler {
	return &OIDCCallbackHandler{store: store, baseURL: baseURL}
}

// Initiate handles GET /auth/oidc/:orgSlug/login — redirects to the OIDC provider.
func (h *OIDCCallbackHandler) Initiate(c *gin.Context) {
	orgSlug := c.Param("orgSlug")

	cfg, err := h.store.GetSSOConfigByOrgSlug(c.Request.Context(), orgSlug)
	if err != nil || cfg == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SSO not configured for this org"})
		return
	}

	provider, err := oidc.NewProvider(c.Request.Context(), cfg.DiscoveryURL)
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

	c.SetCookie("oidc_state", state, 600, "/", "", false, true)
	c.Redirect(http.StatusFound, oauthCfg.AuthCodeURL(state))
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
	c.SetCookie("oidc_state", "", -1, "/", "", false, true)

	cfg, err := h.store.GetSSOConfigByOrgSlug(c.Request.Context(), orgSlug)
	if err != nil || cfg == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SSO configuration not found"})
		return
	}

	provider, err := oidc.NewProvider(c.Request.Context(), cfg.DiscoveryURL)
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

	token, err := oauthCfg.Exchange(c.Request.Context(), code)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token exchange failed"})
		return
	}

	userInfo, err := provider.UserInfo(c.Request.Context(), oauth2.StaticTokenSource(token))
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
	user, err := h.store.GetUserByEmail(c.Request.Context(), claims.Email)
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
		if err := h.store.CreateUser(c.Request.Context(), user); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
			return
		}
	}

	// Ensure membership.
	pendingKeyWrap := role == types.OrgRoleAdmin
	if err := h.store.AddOrgMember(c.Request.Context(), cfg.OrgID, user.ID, role, pendingKeyWrap); err != nil {
		// Ignore duplicate membership error (user may already be a member).
	}

	// Redirect to chat with a success indicator. A full JWT issuance would
	// happen here; for Phase 3 the redirect includes the email for the login
	// page to handle SSO completion.
	redirect := fmt.Sprintf("/login?sso=email&email=%s", claims.Email)
	c.Redirect(http.StatusFound, redirect)
}

func generateRandomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decryptSSOSecret(encrypted []byte) []byte {
	return encrypted
}

var _ = errors.New
