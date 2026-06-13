// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type UsageHandler struct {
	meteringSvc interfaces.MeteringService
	dbSvc       interfaces.DatabaseService
}

func NewUsageHandler(meteringSvc interfaces.MeteringService, dbSvc interfaces.DatabaseService) *UsageHandler {
	return &UsageHandler{
		meteringSvc: meteringSvc,
		dbSvc:       dbSvc,
	}
}

func (h *UsageHandler) GetUsage(c *gin.Context) {
	userID := c.GetString("userID")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	from, to := parsePeriod(c)
	owner := types.BillingOwner{ID: userID, Type: types.OwnerTypeUser}

	report, err := h.meteringSvc.GetUsage(c.Request.Context(), owner, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get usage"})
		return
	}
	c.JSON(http.StatusOK, report)
}

func (h *UsageHandler) GetWorkspaceUsage(c *gin.Context) {
	userID := c.GetString("userID")
	workspaceID := c.Param("id")
	if userID == "" || workspaceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing user or workspace"})
		return
	}

	owned, err := h.dbSvc.CheckResourceOwnership(userID, "workspace", workspaceID)
	if err != nil || !owned {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	from, to := parsePeriod(c)
	owner := types.BillingOwner{ID: userID, Type: types.OwnerTypeUser}

	report, err := h.meteringSvc.GetUsageByWorkspace(c.Request.Context(), owner, workspaceID, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get usage"})
		return
	}
	c.JSON(http.StatusOK, report)
}

func (h *UsageHandler) GetQuotaStatus(c *gin.Context) {
	userID := c.GetString("userID")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	owner := types.BillingOwner{ID: userID, Type: types.OwnerTypeUser}
	statuses, err := h.meteringSvc.GetQuotaStatus(c.Request.Context(), owner)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get quota status"})
		return
	}
	if statuses == nil {
		statuses = []types.QuotaStatus{}
	}
	c.JSON(http.StatusOK, statuses)
}

func (h *UsageHandler) AdminGetUsage(c *gin.Context) {
	ownerID := c.Param("ownerId")
	if ownerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing owner ID"})
		return
	}

	from, to := parsePeriod(c)
	owner := types.BillingOwner{ID: ownerID, Type: types.OwnerTypeUser}

	report, err := h.meteringSvc.GetUsage(c.Request.Context(), owner, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get usage"})
		return
	}
	c.JSON(http.StatusOK, report)
}

func (h *UsageHandler) AdminGetDLQ(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"entries": []interface{}{}})
}

func (h *UsageHandler) AdminRetryDLQ(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *UsageHandler) AdminDiscardDLQ(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *UsageHandler) AdminBillingStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"export_cursors": map[string]interface{}{}, "dlq_size": 0})
}

func parsePeriod(c *gin.Context) (from, to time.Time) {
	now := time.Now()
	fromStr := c.Query("from")
	toStr := c.Query("to")

	if fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			from = t
		}
	}
	if toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			to = t
		}
	}

	if from.IsZero() {
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	if to.IsZero() {
		to = now
	}
	return from, to
}
