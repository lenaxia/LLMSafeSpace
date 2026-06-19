// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/gin-gonic/gin"
	emailsvc "github.com/lenaxia/llmsafespaces/api/internal/services/email"
)

// EmailHandler exposes admin-only email operations (test-send). It wraps the
// EmailService which resolves the configured provider (SES/Noop).
type EmailHandler struct {
	svc *emailsvc.Service
}

// NewEmailHandler constructs an EmailHandler. svc must be non-nil.
func NewEmailHandler(svc *emailsvc.Service) *EmailHandler {
	return &EmailHandler{svc: svc}
}

// TestSend handles POST /api/v1/admin/email/test.
//
// Sends a test email to the given address so an admin can verify the SES
// wiring end-to-end. The endpoint is admin-only (wired behind AdminGuard in
// the router).
//
// Response contract:
//   - SES success:  200 { "sent": true,  "provider": "ses"  }
//   - Noop mode:    200 { "sent": false, "provider": "noop" }
//   - Send failure: 502 { "error": "email send failed: <reason>" }
//
// Noop mode reports sent=false so the admin knows no real email was delivered
// even though the call "succeeded" (NoopProvider logs to stderr and returns nil).
func (h *EmailHandler) TestSend(c *gin.Context) {
	var req struct {
		To string `json:"to" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to is required"})
		return
	}
	addr := strings.TrimSpace(req.To)
	if _, err := mail.ParseAddress(addr); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to must be a valid email address"})
		return
	}

	providerName := h.svc.ProviderName()
	if providerName == "noop" {
		c.JSON(http.StatusOK, gin.H{"sent": false, "provider": "noop"})
		return
	}

	if err := h.svc.SendTest(c.Request.Context(), addr); err != nil {
		if errors.Is(err, emailsvc.ErrNotConfigured) {
			c.JSON(http.StatusOK, gin.H{"sent": false, "provider": "noop"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "email send failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": true, "provider": providerName})
}
