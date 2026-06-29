// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// RefreshWorkspaceCompute re-syncs a workspace CRD with the platform's current
// defaults (resources, security level, storage class, max active sessions) and
// bumps spec.restartGeneration so the controller rebuilds the pod — picking up
// the latest image version (resolved from the RuntimeEnvironment CR at build
// time) and the refreshed resource requests.
//
// Tests cover: happy path (defaults applied + generation bumped), suspended
// workspace (allowed), no settings configured (generation bumped, spec
// unchanged), wrong owner (forbidden), terminal phases (rejected), and K8s
// get/update failures.

func TestRefreshWorkspaceCompute_Active_OverwritesResourcesAndBumpsGeneration(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultResources.cpu":     "1000m",
		"workspace.defaultResources.memory":  "2Gi",
		"workspace.defaultSecurityLevel":     "high",
		"workspace.defaultStorageClass":      "fast-ssd",
		"workspace.defaultMaxActiveSessions": 8,
	})
	ctx := context.Background()

	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	crd.Spec.RestartGeneration = 4
	crd.Spec.Resources = &v1.ResourceRequirements{CPU: "500m", Memory: "512Mi"}
	crd.Spec.SecurityLevel = "standard"
	crd.Spec.MaxActiveSessions = 5
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
	f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.RestartGeneration == 5 &&
			ws.Spec.Resources != nil &&
			ws.Spec.Resources.CPU == "1000m" &&
			ws.Spec.Resources.Memory == "2Gi" &&
			ws.Spec.SecurityLevel == "high" &&
			ws.Spec.Storage.StorageClassName == "fast-ssd" &&
			ws.Spec.MaxActiveSessions == 8
	})).Return(crd, nil)

	res, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
	assert.NoError(t, err)
	assert.Equal(t, int64(5), res.RestartGeneration)
	f.ws.AssertExpectations(t)
	f.ws.AssertNotCalled(t, "UpdateStatus")
}

func TestRefreshWorkspaceCompute_NilResources_InitializedFromDefaults(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultResources.cpu":    "1000m",
		"workspace.defaultResources.memory": "2Gi",
	})
	ctx := context.Background()

	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	crd.Spec.Resources = nil
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
	f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Resources != nil &&
			ws.Spec.Resources.CPU == "1000m" &&
			ws.Spec.Resources.Memory == "2Gi"
	})).Return(crd, nil)

	res, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
	assert.NoError(t, err)
	assert.Equal(t, int64(1), res.RestartGeneration)
}

func TestRefreshWorkspaceCompute_NoSettings_BumpsGenerationOnly(t *testing.T) {
	// No instance settings configured: refresh degrades to a pure pod rebuild
	// (restartGeneration bump). Existing user-set resources are preserved.
	f := newFixture(t)
	ctx := context.Background()

	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	crd.Spec.Resources = &v1.ResourceRequirements{CPU: "2000m", Memory: "4Gi"}
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
	f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.RestartGeneration == 1 &&
			ws.Spec.Resources.CPU == "2000m" &&
			ws.Spec.Resources.Memory == "4Gi"
	})).Return(crd, nil)

	res, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
	assert.NoError(t, err)
	assert.Equal(t, int64(1), res.RestartGeneration)
}

func TestRefreshWorkspaceCompute_Suspended_AllowedAndBumpsGeneration(t *testing.T) {
	// A suspended workspace has no pod; refresh updates spec so the next
	// activate rebuilds with current defaults + image. Bumping restartGeneration
	// is harmless (observed on the next pod build).
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultResources.memory": "2Gi",
	})
	ctx := context.Background()

	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseSuspended
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
	f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.RestartGeneration == 1 && ws.Spec.Resources.Memory == "2Gi"
	})).Return(crd, nil)

	res, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
	assert.NoError(t, err)
	assert.Equal(t, int64(1), res.RestartGeneration)
}

func TestRefreshWorkspaceCompute_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultResources.cpu": "1000m",
	})
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	_, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
	f.ws.AssertNotCalled(t, "Update")
}

func TestRefreshWorkspaceCompute_TerminalPhases_Rejected(t *testing.T) {
	for _, phase := range []v1.WorkspacePhase{v1.WorkspacePhaseTerminating, v1.WorkspacePhaseTerminated} {
		t.Run(string(phase), func(t *testing.T) {
			f := newDefaultsFixture(t, map[string]any{
				"workspace.defaultResources.cpu": "1000m",
			})
			ctx := context.Background()

			crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
			crd.Status.Phase = phase
			f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
			f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)

			_, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
			assert.Error(t, err)
			f.ws.AssertNotCalled(t, "Update")
		})
	}
}

func TestRefreshWorkspaceCompute_K8sGetFails(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultResources.cpu": "1000m",
	})
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return((*v1.Workspace)(nil), errors.New("boom"))

	_, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
	assert.Error(t, err)
	f.ws.AssertNotCalled(t, "Update")
}

func TestRefreshWorkspaceCompute_K8sUpdateFails(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultResources.cpu": "1000m",
	})
	ctx := context.Background()

	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
	f.ws.On("Update", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return((*v1.Workspace)(nil), errors.New("etcd unavailable"))

	_, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_refresh_failed")
}

// === Partial-settings coverage: exercises the conditional logic in ===
// reapplyComputeDefaults where only one of CPU/memory is explicitly configured.
// The settings service returns schema defaults for unconfigured keys (CPU
// default "500m", memory default "1Gi" — from schema.go, NOT registry.go),
// so refresh converges BOTH resources to the platform default: the configured
// one from the admin override, the unconfigured one from the schema default.

func TestRefreshWorkspaceCompute_OnlyCPUConfigured_MemoryGetsSchemaDefault(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultResources.cpu": "1000m",
		// memory default intentionally absent → settings returns schema default "1Gi"
	})
	ctx := context.Background()

	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	crd.Spec.Resources = &v1.ResourceRequirements{CPU: "250m", Memory: "4Gi"}
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
	f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Resources.CPU == "1000m" && ws.Spec.Resources.Memory == "1Gi"
	})).Return(crd, nil)

	_, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestRefreshWorkspaceCompute_OnlyMemoryConfigured_CPUGetsSchemaDefault(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		// cpu default intentionally absent → settings returns schema default "500m"
		"workspace.defaultResources.memory": "2Gi",
	})
	ctx := context.Background()

	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	crd.Spec.Resources = &v1.ResourceRequirements{CPU: "2000m", Memory: "512Mi"}
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
	f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Resources.CPU == "500m" && ws.Spec.Resources.Memory == "2Gi"
	})).Return(crd, nil)

	_, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

// === Non-terminal phase coverage: only Terminating/Terminated are rejected;
// every other phase is allowed (refresh updates spec for the next pod build).
// Active and Suspended are covered above; this covers the remaining phases.

func TestRefreshWorkspaceCompute_NonTerminalPhases_AllowedAndBumpsGeneration(t *testing.T) {
	for _, phase := range []v1.WorkspacePhase{
		v1.WorkspacePhasePending,
		v1.WorkspacePhaseCreating,
		v1.WorkspacePhaseResuming,
		v1.WorkspacePhaseFailed,
		v1.WorkspacePhaseSuspending,
	} {
		t.Run(string(phase), func(t *testing.T) {
			f := newDefaultsFixture(t, map[string]any{
				"workspace.defaultResources.cpu": "1000m",
			})
			ctx := context.Background()

			crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
			crd.Status.Phase = phase
			f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
			f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
			f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
				return ws.Spec.RestartGeneration == 1 &&
					ws.Spec.Resources != nil && ws.Spec.Resources.CPU == "1000m"
			})).Return(crd, nil)

			res, err := f.svc.RefreshWorkspaceCompute(ctx, "user1", "ws-1")
			assert.NoError(t, err)
			assert.Equal(t, int64(1), res.RestartGeneration)
		})
	}
}
