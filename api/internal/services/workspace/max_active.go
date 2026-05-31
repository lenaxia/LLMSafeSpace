// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"fmt"
	"sort"

	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/settings"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// SetInstanceSettings injects the instance settings service for enforcement.
func (s *Service) SetInstanceSettings(svc *settings.InstanceService) {
	s.instanceSettings = svc
}

// enforceMaxActiveWorkspaces checks if the user is at their active workspace
// cap and suspends the stalest workspace if needed. Returns the ID of the
// suspended workspace (empty if none was suspended).
func (s *Service) enforceMaxActiveWorkspaces(ctx context.Context, userID, targetWorkspaceID string) (string, error) {
	if s.instanceSettings == nil {
		return "", nil
	}

	maxActive, err := s.instanceSettings.GetInt(ctx, "workspace.maxActiveWorkspacesPerUser")
	if err != nil {
		// If settings unavailable, don't block activation
		s.logger.Warn("failed to read maxActiveWorkspacesPerUser, skipping enforcement",
			"error", err.Error(),
		)
		return "", nil
	}

	// List user's workspaces (DB rows for ordering by UpdatedAt) and fetch
	// the live phase from CRDs. Phase is owned by the CRD; the DB no longer
	// caches it.
	result, _, err := s.dbService.ListWorkspaces(ctx, userID, 100, 0)
	if err != nil {
		return "", fmt.Errorf("failed to list workspaces for enforcement: %w", err)
	}

	phaseByID := s.fetchUserWorkspacePhases(ctx, userID)

	var active []*types.WorkspaceMetadata
	for _, ws := range result {
		if ws.ID == targetWorkspaceID {
			continue
		}
		if isActivePhase(v1.WorkspacePhase(phaseByID[ws.ID])) {
			active = append(active, ws)
		}
	}

	if len(active) < maxActive {
		return "", nil
	}

	// Sort by last activity (oldest first) for stalest selection
	sort.Slice(active, func(i, j int) bool {
		ti := active[i].UpdatedAt
		tj := active[j].UpdatedAt
		return ti.Before(tj)
	})

	// Suspend the stalest workspace
	stalest := active[0]
	if err := s.SuspendWorkspace(ctx, userID, stalest.ID); err != nil {
		return "", fmt.Errorf("failed to suspend stalest workspace %s: %w", stalest.ID, err)
	}

	s.logger.Info("auto-suspended workspace due to max active limit",
		"suspended_workspace", stalest.ID,
		"user_id", userID,
		"max_active", maxActive,
	)

	return stalest.ID, nil
}

// enforceMaxStorageSize validates that the requested storage size does not
// exceed the configured maximum. Returns a validation error if exceeded.
func (s *Service) enforceMaxStorageSize(ctx context.Context, requestedSize string) error {
	if s.instanceSettings == nil {
		return nil
	}

	maxSize, err := s.instanceSettings.GetString(ctx, "workspace.maxStorageSize")
	if err != nil || maxSize == "" {
		return nil // settings unavailable, don't block
	}

	reqBytes := parseStorageSize(requestedSize)
	maxBytes := parseStorageSize(maxSize)

	if reqBytes <= 0 || maxBytes <= 0 {
		return nil // unparseable, don't block
	}

	if reqBytes > maxBytes {
		return apierrors.NewValidationError(
			fmt.Sprintf("storage size %s exceeds maximum %s", requestedSize, maxSize),
			map[string]interface{}{"field": "storageSize", "max": maxSize},
			fmt.Errorf("requested %s > max %s", requestedSize, maxSize),
		)
	}
	return nil
}

// parseStorageSize converts a K8s quantity string (e.g. "1Gi", "512Mi") to bytes.
func parseStorageSize(s string) int64 {
	if len(s) < 3 {
		return 0
	}
	suffix := s[len(s)-2:]
	numStr := s[:len(s)-2]
	var n int64
	for _, c := range numStr {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	switch suffix {
	case "Gi":
		return n * 1024 * 1024 * 1024
	case "Mi":
		return n * 1024 * 1024
	default:
		return 0
	}
}

func isActivePhase(p v1.WorkspacePhase) bool {
	return p == v1.WorkspacePhaseActive || p == v1.WorkspacePhaseCreating || p == v1.WorkspacePhaseResuming
}
