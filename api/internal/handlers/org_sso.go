// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/api/internal/services/sso"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// ssoStore is the org-data subset used by the SSO CRUD + discovery endpoints.
type ssoStore interface {
	GetSSOConfig(ctx context.Context, orgID string) (*types.OrgSSOConfig, error)
	DeleteSSOConfig(ctx context.Context, orgID string) error
	ListSSODomains(ctx context.Context) ([]types.SSODomain, error)
	CountSSOConfigs(ctx context.Context) (int, error)
	LogOrgEvent(ctx context.Context, orgID, actorID, action, targetID string, metadata map[string]any) error
}

type ssoLogger interface {
	Warn(msg string, args ...any)
}

// SSOHandler exposes both the org-admin SSO config CRUD and the public OIDC
// login flow (start/callback) and domain discovery. The OIDC mechanics live in
// the sso.Service; this handler owns HTTP concerns (cookies, redirects,
// response shaping).
type SSOHandler struct {
	svc           *sso.Service
	store         ssoStore
	authSvc       orgAuthService
	sessionCookie string
	frontendURL   string
	logger        ssoLogger
}

// NewSSOHandler constructs the handler. sessionCookie is the JWT cookie name
// used elsewhere in the app (e.g. "lsp_session"); frontendURL is the post-SSO
// browser landing URL (may be empty — the handler falls back to "/").
func NewSSOHandler(svc *sso.Service, store ssoStore, authSvc orgAuthService, sessionCookie, frontendURL string, logger ssoLogger) *SSOHandler {
	return &SSOHandler{
		svc:           svc,
		store:         store,
		authSvc:       authSvc,
		sessionCookie: sessionCookie,
		frontendURL:   frontendURL,
		logger:        logger,
	}
}

// toResponse projects the at-rest config into the API shape, omitting the
// encrypted client secret (HasSecret replaces it).
func toSSOResponse(cfg *types.OrgSSOConfig) types.OrgSSOConfigResponse {
	resp := types.OrgSSOConfigResponse{
		OrgID:            cfg.OrgID,
		DiscoveryURL:     cfg.DiscoveryURL,
		ClientID:         cfg.ClientID,
		HasSecret:        len(cfg.ClientSecret) > 0,
		ClaimedDomains:   cfg.ClaimedDomains,
		AutoProvision:    cfg.AutoProvision,
		GroupRoleMapping: cfg.GroupRoleMapping,
		UpdatedAt:        cfg.UpdatedAt,
	}
	if resp.ClaimedDomains == nil {
		resp.ClaimedDomains = []string{}
	}
	if resp.GroupRoleMapping == nil {
		resp.GroupRoleMapping = map[string]types.OrgRole{}
	}
	return resp
}

// Get handles GET /api/v1/orgs/:id/sso (org admin).
func (h *SSOHandler) Get(c *gin.Context) {
	orgID := c.Param("id")
	cfg, err := h.store.GetSSOConfig(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load sso config"})
		return
	}
	if cfg == nil {
		c.JSON(http.StatusOK, types.OrgSSOConfigResponse{
			OrgID:            orgID,
			ClaimedDomains:   []string{},
			GroupRoleMapping: map[string]types.OrgRole{},
			AutoProvision:    true,
		})
		return
	}
	c.JSON(http.StatusOK, toSSOResponse(cfg))
}

// Put handles PUT /api/v1/orgs/:id/sso (org admin). Upserts the SSO config.
func (h *SSOHandler) Put(c *gin.Context) {
	orgID := c.Param("id")
	actorID := h.authSvc.GetUserID(c)

	var req types.UpsertSSOConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Existing config provides the current encrypted secret for the
	// "empty clientSecret = leave unchanged" partial-update path.
	existing, err := h.store.GetSSOConfig(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load existing sso config"})
		return
	}
	var existingSecret []byte
	if existing != nil {
		existingSecret = existing.ClientSecret
	}

	if _, err := h.svc.ApplyConfigMutation(c.Request.Context(), orgID, req, existingSecret); err != nil {
		respondSSOMutationError(c, err)
		return
	}

	// Reload the stored row so the response reflects DB timestamps.
	cfg, err := h.store.GetSSOConfig(c.Request.Context(), orgID)
	if err != nil || cfg == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	if err := h.store.LogOrgEvent(c.Request.Context(), orgID, actorID, "sso.update", orgID, map[string]any{
		"discoveryUrl": cfg.DiscoveryURL, "autoProvision": cfg.AutoProvision,
	}); err != nil && h.logger != nil {
		h.logger.Warn("audit log emission failed", "action", "sso.update", "orgID", orgID, "error", err.Error())
	}
	c.JSON(http.StatusOK, toSSOResponse(cfg))
}

// Delete handles DELETE /api/v1/orgs/:id/sso (org admin).
func (h *SSOHandler) Delete(c *gin.Context) {
	orgID := c.Param("id")
	actorID := h.authSvc.GetUserID(c)
	if err := h.store.DeleteSSOConfig(c.Request.Context(), orgID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete sso config"})
		return
	}
	if err := h.store.LogOrgEvent(c.Request.Context(), orgID, actorID, "sso.delete", orgID, nil); err != nil && h.logger != nil {
		h.logger.Warn("audit log emission failed", "action", "sso.delete", "orgID", orgID, "error", err.Error())
	}
	c.Status(http.StatusNoContent)
}

// Start handles GET /api/v1/auth/sso/:orgSlug/start (public). Redirects the
// browser to the IdP authorization endpoint and sets the signed PKCE/state cookie.
func (h *SSOHandler) Start(c *gin.Context) {
	orgSlug := c.Param("orgSlug")
	redirectURL := h.resolveCallbackURL(c, orgSlug)

	res, err := h.svc.StartLogin(c.Request.Context(), orgSlug, redirectURL)
	if err != nil {
		// Only the sentinel "not configured" reason is user-meaningful; any
		// wrapped DB/discovery error is logged internally and surfaced as a
		// generic message to avoid leaking internals.
		if errors.Is(err, sso.ErrSSONotConfigured) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if h.logger != nil {
			h.logger.Warn("sso start failed", "orgSlug", orgSlug, "error", err.Error())
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "single sign-on is currently unavailable"})
		return
	}
	h.setStateCookie(c, res.Cookie)
	c.Redirect(http.StatusFound, res.AuthURL)
}

// Callback handles GET /api/v1/auth/sso/:orgSlug/callback (public). Exchanges
// the code, sets the session JWT cookie, and redirects to the frontend.
func (h *SSOHandler) Callback(c *gin.Context) {
	orgSlug := c.Param("orgSlug")
	redirectURL := h.resolveCallbackURL(c, orgSlug)
	code := c.Query("code")
	state := c.Query("state")
	cookieValue, _ := c.Cookie(h.svc.CookieName())

	result, err := h.svc.HandleCallback(c.Request.Context(), orgSlug, redirectURL, code, state, cookieValue)
	h.clearStateCookie(c)
	if err != nil {
		c.Redirect(http.StatusFound, h.frontendRedirectWithError(err))
		return
	}

	maxAge := int(h.svc.TokenTTL().Seconds())
	if maxAge <= 0 {
		maxAge = 86400
	}
	c.SetCookie(h.sessionCookie, result.Token, maxAge, "/", "", true, true)
	c.Redirect(http.StatusFound, h.frontendRedirectWithSuccess())
}

// Domains handles GET /api/v1/auth/sso/domains (public). Returns every claimed
// SSO domain for login-page discovery.
func (h *SSOHandler) Domains(c *gin.Context) {
	domains, err := h.store.ListSSODomains(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sso domains"})
		return
	}
	if domains == nil {
		domains = []types.SSODomain{}
	}
	c.JSON(http.StatusOK, gin.H{"domains": domains})
}

// OIDCEnabled reports whether any org has configured SSO, for the /auth/config
// feature flag. A DB error is treated as "disabled" so a transient failure does
// not advertise SSO when there is none to complete.
func (h *SSOHandler) OIDCEnabled(ctx context.Context) bool {
	if h.store == nil {
		return false
	}
	n, err := h.store.CountSSOConfigs(ctx)
	if err != nil {
		return false
	}
	return n > 0
}

// resolveCallbackURL builds the absolute IdP-registered callback URL for the
// given org slug. OIDC.RedirectBaseURL wins when set; otherwise it is derived
// from the incoming request (X-Forwarded-Proto aware).
func (h *SSOHandler) resolveCallbackURL(c *gin.Context, orgSlug string) string {
	path := "/api/v1/auth/sso/" + orgSlug + "/callback"
	if base := h.svc.RedirectBaseURL(); base != "" {
		return strings.TrimRight(base, "/") + path
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + c.Request.Host + path
}

func (h *SSOHandler) setStateCookie(c *gin.Context, cookie *sso.SignedCookie) {
	maxAge := int(cookie.MaxAge.Seconds())
	// SameSite=Lax so the cookie survives the top-level IdP → callback GET
	// redirect while staying unavailable to cross-site POST/script fetches.
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(cookie.Name, cookie.Value, maxAge, "/", "", true, true)
}

func (h *SSOHandler) clearStateCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(h.svc.CookieName(), "", -1, "/", "", true, true)
}

func (h *SSOHandler) frontendRedirectWithSuccess() string {
	return h.appendSSOParam(h.frontendURL, "success")
}

func (h *SSOHandler) frontendRedirectWithError(err error) string {
	return h.appendSSOParam(h.frontendURL, errorReason(err))
}

func (h *SSOHandler) appendSSOParam(base, status string) string {
	if base == "" {
		base = "/"
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "sso=" + status
}

// errorReason maps an SSO service error to a short, client-safe status token.
// It never leaks internal detail.
func errorReason(err error) string {
	switch {
	case errors.Is(err, sso.ErrAutoProvisionOff):
		return "provisioning_disabled"
	case errors.Is(err, sso.ErrUserSuspended):
		return "suspended"
	case errors.Is(err, sso.ErrStateExpired), errors.Is(err, sso.ErrStateInvalid):
		return "state_invalid"
	default:
		return "error"
	}
}

// respondSSOMutationError maps ApplyConfigMutation errors to HTTP responses.
func respondSSOMutationError(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "server key not configured"):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": msg})
	case strings.Contains(msg, "client secret is required"):
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
	case strings.Contains(msg, "invalid role"):
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save sso config"})
	}
}
