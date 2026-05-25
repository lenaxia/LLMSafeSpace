package types

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
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
