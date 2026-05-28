package workspace

import (
	"context"
	"fmt"
	"sort"

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

	// List user's workspaces to find active ones
	result, _, err := s.dbService.ListWorkspaces(ctx, userID, 100, 0)
	if err != nil {
		return "", fmt.Errorf("failed to list workspaces for enforcement: %w", err)
	}

	// Count active (Running) workspaces, excluding the target
	var active []*types.WorkspaceMetadata
	for _, ws := range result {
		if ws.ID == targetWorkspaceID {
			continue
		}
		if ws.Phase == "Running" || ws.Phase == "Active" {
			active = append(active, ws)
		}
	}

	// If under the cap, no action needed
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
