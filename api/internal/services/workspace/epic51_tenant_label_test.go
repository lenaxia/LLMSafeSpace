// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

// Epic 51 S51.3 — API-side tenant label tests.
//
// Verifies that buildWorkspaceCRD sets the correct tenant label and
// that user-supplied labels cannot spoof the system tenant identity.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

func TestS51_3_BuildCRD_TenantLabel_UserOnly(t *testing.T) {
	req := types.CreateWorkspaceRequest{
		Name:   "my-ws",
		Labels: map[string]string{"env": "test"},
	}

	crd := buildWorkspaceCRD("ws-123", "user-abc", req, "default")

	require.Equal(t, "user-abc", crd.Labels["llmsafespaces.dev/tenant"],
		"tenant label must be userID when no orgID is set")
	require.Equal(t, "test", crd.Labels["env"],
		"user-supplied labels must be preserved")
}

func TestS51_3_BuildCRD_TenantLabel_OrgMember(t *testing.T) {
	orgID := "org-xyz"
	req := types.CreateWorkspaceRequest{
		Name:   "my-ws",
		OrgID:  &orgID,
		Labels: map[string]string{"env": "test"},
	}

	crd := buildWorkspaceCRD("ws-123", "user-abc", req, "default")

	require.Equal(t, "org-xyz", crd.Labels["llmsafespaces.dev/tenant"],
		"tenant label must be orgID for org members (Design 0031 D4)")
}

func TestS51_3_BuildCRD_TenantLabel_CannotBeSpoofed(t *testing.T) {
	req := types.CreateWorkspaceRequest{
		Name:   "my-ws",
		Labels: map[string]string{"llmsafespaces.dev/tenant": "victim-org"},
	}

	crd := buildWorkspaceCRD("ws-123", "user-abc", req, "default")

	require.Equal(t, "user-abc", crd.Labels["llmsafespaces.dev/tenant"],
		"system tenant label must not be overridable by user-supplied labels")
}

func TestS51_3_BuildCRD_SystemLabels_CannotBeSpoofed(t *testing.T) {
	req := types.CreateWorkspaceRequest{
		Name: "my-ws",
		Labels: map[string]string{
			"app":                       "malicious",
			"user-id":                   "victim-user",
			"llmsafespaces.dev/tenant":  "victim-org",
			"llmsafespaces.dev/workspace": "stolen-id",
		},
	}

	crd := buildWorkspaceCRD("ws-123", "user-abc", req, "default")

	require.Equal(t, "llmsafespaces", crd.Labels["app"],
		"system 'app' label must not be overridable")
	require.Equal(t, "user-abc", crd.Labels["user-id"],
		"system 'user-id' label must not be overridable (prevents cross-tenant info disclosure)")
	require.Equal(t, "user-abc", crd.Labels["llmsafespaces.dev/tenant"],
		"system tenant label must not be overridable")
}
