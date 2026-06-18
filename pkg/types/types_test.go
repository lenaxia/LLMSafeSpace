// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceType_HasExpectedFields(t *testing.T) {
	typ := reflect.TypeOf(Workspace{})
	expectedFields := []string{"ID", "Name", "UserID", "Runtime", "StorageSize", "Phase", "PVCName", "Labels", "CreatedAt", "UpdatedAt"}
	for _, name := range expectedFields {
		_, ok := typ.FieldByName(name)
		assert.True(t, ok, "Workspace should have field %s", name)
	}
}

func TestEnsureSessionResponse_HasWorkspaceFields(t *testing.T) {
	typ := reflect.TypeOf(EnsureSessionResponse{})
	_, ok := typ.FieldByName("WorkspaceID")
	assert.True(t, ok, "EnsureSessionResponse should have WorkspaceID")
	_, ok = typ.FieldByName("WorkspacePhase")
	assert.True(t, ok, "EnsureSessionResponse should have WorkspacePhase")
	_, ok = typ.FieldByName("SessionID")
	assert.True(t, ok)
}

func TestCachedSession_HasWorkspaceID(t *testing.T) {
	typ := reflect.TypeOf(CachedSession{})
	_, ok := typ.FieldByName("WorkspaceID")
	assert.True(t, ok, "CachedSession should have WorkspaceID not SandboxID")
}

// WorkspaceMetadata is the persisted DB record. Phase and PVCState used to be
// cached here but were removed because the CRD is the source of truth and the
// cache caused divergence (see migration 9). This test guards against
// re-introducing those fields by accident.
func TestWorkspaceMetadata_DoesNotCachePhaseOrPVCState(t *testing.T) {
	typ := reflect.TypeOf(WorkspaceMetadata{})
	for _, forbidden := range []string{"Phase", "PVCState"} {
		_, ok := typ.FieldByName(forbidden)
		assert.False(t, ok,
			"WorkspaceMetadata.%s must not exist; phase/pvc state are owned by the Workspace CRD", forbidden)
	}
}

// TestWorkspaceMetaFromCtx covers the neutral context-accessor used by both
// WorkspaceAccessMiddleware (gin layer) and workspace.Service.verifyOwner
// (service layer) so the two never diverge on the key or value shape. Keeping
// the accessor next to the key in pkg/types avoids a layering inversion
// (service importing the HTTP middleware package).
func TestWorkspaceMetaFromCtx(t *testing.T) {
	t.Run("missing_returns_false_nil", func(t *testing.T) {
		got, ok := WorkspaceMetaFromCtx(context.Background())
		assert.False(t, ok)
		assert.Nil(t, got)
	})

	t.Run("wrong_type_returns_false_nil", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyWorkspaceMeta, "not a meta")
		got, ok := WorkspaceMetaFromCtx(ctx)
		assert.False(t, ok)
		assert.Nil(t, got)
	})

	t.Run("nil_value_returns_false_nil", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ContextKeyWorkspaceMeta, (*WorkspaceMetadata)(nil))
		got, ok := WorkspaceMetaFromCtx(ctx)
		assert.False(t, ok)
		assert.Nil(t, got)
	})

	t.Run("present_returns_meta_true", func(t *testing.T) {
		want := &WorkspaceMetadata{ID: "ws-1", UserID: "user-1"}
		ctx := context.WithValue(context.Background(), ContextKeyWorkspaceMeta, want)
		got, ok := WorkspaceMetaFromCtx(ctx)
		require.True(t, ok)
		require.NotNil(t, got)
		assert.Same(t, want, got)
	})
}
