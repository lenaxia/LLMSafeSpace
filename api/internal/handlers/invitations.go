// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/lenaxia/llmsafespaces/pkg/email"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

const (
	invitationExpiry     = 7 * 24 * time.Hour
	maxInvitationsPerHr  = 50
	invitationTokenBytes = 32
)

// invitationStore is the data-access surface for invitations + the org/member
// lookups needed to build InvitationDetail.
type invitationStore interface {
	CreateInvitation(ctx context.Context, inv *types.OrgInvitation) error
	ListPendingInvitations(ctx context.Context, orgID string) ([]*types.OrgInvitation, error)
	GetInvitationByTokenHash(ctx context.Context, tokenHash string) (*types.OrgInvitation, error)
	GetInvitationByID(ctx context.Context, invID string) (*types.OrgInvitation, error)
	AcceptInvitationTx(ctx context.Context, invID, userID string, role types.OrgRole) (*types.OrgMember, bool, error)
	DeclineInvitation(ctx context.Context, invID string) error
	DeleteInvitation(ctx context.Context, invID string) error
	CountInvitationsLastHour(ctx context.Context, orgID string) (int, error)
	GetOrg(ctx context.Context, orgID string) (*types.Organization, error)
	GetOrgMember(ctx context.Context, orgID, userID string) (*types.OrgMember, error)
	// GetUserOrgID returns the user's single org ID (or "" if not in any org).
	// Used by invitation acceptance to enforce single-org membership (S3/D8).
	GetUserOrgID(ctx context.Context, userID string) (string, error)
	// GetUserEmail resolves a user ID to their email address. Used by invitation
	// acceptance to verify the accepting user matches the invited email.
	GetUserEmail(ctx context.Context, userID string) (string, error)
}

// orgCredentialBinder binds org credentials to org workspaces. Used after
// invitation acceptance (F7) to seed org credentials into newly-migrated
// workspaces. Optional — nil in dev mode (no secret store wired).
type orgCredentialBinder interface {
	BindAllOrgCredentialsToOrgWorkspaces(ctx context.Context, orgID string) error
}

// InvitationsHandler handles org invitation CRUD and the accept/decline flows.
type InvitationsHandler struct {
	store          invitationStore
	email          email.EmailProvider
	authSvc        orgAuthService
	baseURL        string
	logger         invitationLogger
	credentialBind orgCredentialBinder
}

type invitationLogger interface {
	Warn(msg string, args ...any)
	Error(msg string, err error, args ...any)
}

// NewInvitationsHandler constructs the handler. email may be nil in which case
// Create/Resend succeed but no email is sent (dev mode). logger may be nil.
func NewInvitationsHandler(store invitationStore, mailer email.EmailProvider, authSvc orgAuthService, baseURL string, logger invitationLogger) *InvitationsHandler {
	return &InvitationsHandler{store: store, email: mailer, authSvc: authSvc, baseURL: baseURL, logger: logger}
}

// SetCredentialBinder wires the org credential binder used after invitation
// acceptance (F7). Optional — nil means no credential seeding on join.
func (h *InvitationsHandler) SetCredentialBinder(b orgCredentialBinder) {
	h.credentialBind = b
}

// Create handles POST /api/v1/orgs/:id/invitations.
func (h *InvitationsHandler) Create(c *gin.Context) {
	orgID := c.Param("id")
	invitedBy := h.authSvc.GetUserID(c)
	ctx := c.Request.Context()

	var req types.CreateInvitationsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Role != types.OrgRoleAdmin && req.Role != types.OrgRoleMember {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be 'admin' or 'member'"})
		return
	}

	count, err := h.store.CountInvitationsLastHour(ctx, orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check rate limit"})
		return
	}
	if count+len(req.Emails) > maxInvitationsPerHr {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "invitation rate limit exceeded", "limit": maxInvitationsPerHr})
		return
	}

	created := make([]*types.OrgInvitation, 0, len(req.Emails))
	org, err := h.store.GetOrg(ctx, orgID)
	if err != nil || org == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get organization"})
		return
	}
	for _, addr := range req.Emails {
		token, hash, err := generateInvitationToken()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
			return
		}
		inv := &types.OrgInvitation{
			ID:        uuid.New().String(),
			OrgID:     orgID,
			Email:     addr,
			Role:      req.Role,
			InvitedBy: invitedBy,
			TokenHash: hash,
			ExpiresAt: time.Now().Add(invitationExpiry),
		}
		if err := h.store.CreateInvitation(ctx, inv); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create invitation", "email": addr})
			return
		}
		created = append(created, inv)
		h.sendInvitationEmail(ctx, addr, token, org.Name, orgID, req.Role)
	}

	c.JSON(http.StatusCreated, created)
}

// List handles GET /api/v1/orgs/:id/invitations.
func (h *InvitationsHandler) List(c *gin.Context) {
	orgID := c.Param("id")
	invitations, err := h.store.ListPendingInvitations(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list invitations"})
		return
	}
	c.JSON(http.StatusOK, invitations)
}

// Delete handles DELETE /api/v1/orgs/:id/invitations/:invID.
func (h *InvitationsHandler) Delete(c *gin.Context) {
	orgID := c.Param("id")
	invID := c.Param("invID")
	existing, err := h.store.GetInvitationByID(c.Request.Context(), invID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get invitation"})
		return
	}
	if existing == nil || existing.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}
	if err := h.store.DeleteInvitation(c.Request.Context(), invID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke invitation"})
		return
	}
	c.Status(http.StatusNoContent)
}

// Resend handles POST /api/v1/orgs/:id/invitations/:invID/resend. Generates a
// new token (invalidating the old one), resets the expiry, and re-sends.
func (h *InvitationsHandler) Resend(c *gin.Context) {
	orgID := c.Param("id")
	invID := c.Param("invID")
	ctx := c.Request.Context()

	existing, err := h.store.GetInvitationByID(ctx, invID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get invitation"})
		return
	}
	if existing == nil || existing.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}
	if existing.AcceptedAt != nil || existing.DeclinedAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot resend an already accepted or declined invitation"})
		return
	}
	if existing.BounceType == "permanent" || existing.BounceType == "complaint" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email address has a permanent bounce; cannot resend"})
		return
	}

	token, hash, err := generateInvitationToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}
	inv := &types.OrgInvitation{
		ID:        uuid.New().String(),
		OrgID:     orgID,
		Email:     existing.Email,
		Role:      existing.Role,
		InvitedBy: existing.InvitedBy,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(invitationExpiry),
	}
	if err := h.store.CreateInvitation(ctx, inv); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create invitation"})
		return
	}
	// Invalidate the old invitation AFTER the new one is persisted so a
	// failure between create and delete doesn't lose the invitation.
	_ = h.store.DeleteInvitation(ctx, invID)
	h.sendInvitationEmail(ctx, existing.Email, token, "", orgID, existing.Role)

	c.JSON(http.StatusOK, inv)
}

// GetByToken handles GET /api/v1/invitations/:token (public — no auth).
func (h *InvitationsHandler) GetByToken(c *gin.Context) {
	token := c.Param("token")
	ctx := c.Request.Context()

	hash := hashToken(token)
	inv, err := h.store.GetInvitationByTokenHash(ctx, hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get invitation"})
		return
	}
	if inv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}

	org, err := h.store.GetOrg(ctx, inv.OrgID)
	if err != nil || org == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}

	inviterName := "An administrator"
	if member, err := h.store.GetOrgMember(ctx, inv.OrgID, inv.InvitedBy); err == nil && member != nil && member.Username != "" {
		inviterName = member.Username
	}

	c.JSON(http.StatusOK, types.InvitationDetail{
		OrgName:     org.Name,
		OrgSlug:     org.Slug,
		InviterName: inviterName,
		Role:        inv.Role,
		ExpiresAt:   inv.ExpiresAt,
	})
}

// Accept handles POST /api/v1/invitations/:token/accept (JWT required).
func (h *InvitationsHandler) Accept(c *gin.Context) {
	token := c.Param("token")
	userID := h.authSvc.GetUserID(c)
	ctx := c.Request.Context()

	hash := hashToken(token)
	inv, err := h.store.GetInvitationByTokenHash(ctx, hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get invitation"})
		return
	}
	if inv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}
	if inv.AcceptedAt != nil || inv.DeclinedAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "invitation already accepted or declined"})
		return
	}
	if time.Now().After(inv.ExpiresAt) {
		c.JSON(http.StatusGone, gin.H{"error": "invitation expired"})
		return
	}

	existing, err := h.store.GetOrgMember(ctx, inv.OrgID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check membership"})
		return
	}
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "user is already a member of this org"})
		return
	}

	// Single-org enforcement (D8/S3): with the unique index on
	// org_memberships(user_id), a user in a different org would hit a raw DB
	// constraint violation on insert. Pre-check here to return a clear 409.
	currentOrgID, err := h.store.GetUserOrgID(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check existing org membership"})
		return
	}
	if currentOrgID != "" {
		c.JSON(http.StatusConflict, gin.H{"error": "user is already a member of another organization"})
		return
	}

	// Verify the accepting user's email matches the invited email. This prevents
	// token theft from granting org membership to an attacker who controls a
	// different account.
	userEmail, err := h.store.GetUserEmail(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify user email"})
		return
	}
	if !strings.EqualFold(userEmail, inv.Email) {
		c.JSON(http.StatusForbidden, gin.H{"error": "this invitation was sent to a different email address"})
		return
	}

	member, alreadyTaken, err := h.store.AcceptInvitationTx(ctx, inv.ID, userID, inv.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to accept invitation"})
		return
	}
	if alreadyTaken {
		c.JSON(http.StatusConflict, gin.H{"error": "invitation already accepted or declined"})
		return
	}
	if member == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}

	// F7: bind all org credentials to the newly-attributed workspaces. The
	// workspace migration happened inside AcceptInvitationTx (D4); this step
	// seeds the org's shared credentials into those workspaces immediately.
	// Fire-and-forget: runs in a background goroutine so the user's accept
	// response isn't blocked by the CROSS JOIN. Credentials will also bind on
	// the next credential reload if this goroutine fails.
	if h.credentialBind != nil {
		orgID := inv.OrgID
		uid := userID
		logger := h.logger
		go func() {
			if err := h.credentialBind.BindAllOrgCredentialsToOrgWorkspaces(context.Background(), orgID); err != nil && logger != nil {
				logger.Error("failed to bind org credentials after invitation accept", err, "orgID", orgID, "userID", uid)
			}
		}()
	}

	c.JSON(http.StatusOK, gin.H{"membership": member})
}

// Decline handles POST /api/v1/invitations/:token/decline (JWT required).
func (h *InvitationsHandler) Decline(c *gin.Context) {
	token := c.Param("token")
	ctx := c.Request.Context()

	hash := hashToken(token)
	inv, err := h.store.GetInvitationByTokenHash(ctx, hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get invitation"})
		return
	}
	if inv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}

	if err := h.store.DeclineInvitation(ctx, inv.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decline invitation"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "declined"})
}

func (h *InvitationsHandler) sendInvitationEmail(ctx context.Context, addr, token, orgName string, orgID string, role types.OrgRole) {
	if h.email == nil {
		return
	}
	if orgName == "" {
		org, err := h.store.GetOrg(ctx, orgID)
		if err != nil || org == nil {
			return
		}
		orgName = org.Name
	}
	link := fmt.Sprintf("%s/invitations/%s", strings.TrimRight(h.baseURL, "/"), token)
	subject := fmt.Sprintf("[%s] Invitation to join on LLMSafeSpaces", orgName)
	escapedOrgName := html.EscapeString(orgName)
	textBody := fmt.Sprintf("You've been invited to join %s as a %s.\n\nClick here to accept: %s\n\nThis invitation expires in 7 days.", orgName, role, link)
	htmlBody := fmt.Sprintf("<p>You've been invited to join <strong>%s</strong> as a <strong>%s</strong>.</p><p><a href=\"%s\">Click here to accept</a></p><p>This invitation expires in 7 days.</p>", escapedOrgName, role, link)

	if err := h.email.Send(ctx, email.Message{
		To:       addr,
		Subject:  subject,
		TextBody: textBody,
		HTMLBody: htmlBody,
	}); err != nil && h.logger != nil {
		h.logger.Error("invitation email send failed", err, "to", addr, "orgID", orgID)
	}
}

func generateInvitationToken() (token string, hash string, err error) {
	raw := make([]byte, invitationTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	hash = hashToken(token)
	return token, hash, nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return base64.StdEncoding.EncodeToString(h[:])
}
