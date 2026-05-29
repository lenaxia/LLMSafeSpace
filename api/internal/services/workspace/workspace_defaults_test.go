package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/settings"
	"github.com/lenaxia/llmsafespace/pkg/types"

	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
)

// inMemorySettingsStore implements settings.InstanceStore for testing.
type inMemorySettingsStore struct {
	data map[string]json.RawMessage
}

func (s *inMemorySettingsStore) GetAllInstanceSettings(_ context.Context) (map[string]json.RawMessage, error) {
	return s.data, nil
}

func (s *inMemorySettingsStore) SetInstanceSetting(_ context.Context, key string, value json.RawMessage) error {
	s.data[key] = value
	return nil
}

func newTestSettings(vals map[string]any) *settings.InstanceService {
	data := make(map[string]json.RawMessage)
	for k, v := range vals {
		raw, _ := json.Marshal(v)
		data[k] = raw
	}
	var log pkginterfaces.LoggerInterface = lmocks.NewMockLogger()
	svc := settings.NewInstanceService(&inMemorySettingsStore{data: data}, log)
	svc.Start()
	return svc
}

func newDefaultsFixture(t *testing.T, settingsData map[string]any) *fixture {
	t.Helper()
	f := newFixture(t)
	if settingsData != nil {
		f.svc.SetInstanceSettings(newTestSettings(settingsData))
	}
	return f
}

// === US-13.0: workspace.defaultImage ===

func TestCreateWorkspace_EmptyRuntime_UsesDefaultImage(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultImage": "ghcr.io/lenaxia/llmsafespace/base:v2",
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Runtime == "ghcr.io/lenaxia/llmsafespace/base:v2"
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: ""}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_ExplicitRuntime_NotOverridden(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultImage": "ghcr.io/lenaxia/llmsafespace/base:v2",
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Runtime == "python:3.11"
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "python:3.11"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_NoSettings_EmptyRuntimePassesThrough(t *testing.T) {
	f := newDefaultsFixture(t, nil) // no settings
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Runtime == ""
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
}

// === US-13.1: workspace.defaultStorageSize ===

func TestCreateWorkspace_EmptyStorageSize_UsesDefault(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultStorageSize": "2Gi",
		"workspace.maxStorageSize":     "10Gi",
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Storage.Size == "2Gi"
	})).Return(crdWorkspace("ws-1", "default", "user1", "2Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_EmptyStorageSize_NoSettings_FailsValidation(t *testing.T) {
	f := newDefaultsFixture(t, nil)
	ctx := context.Background()

	req := types.CreateWorkspaceRequest{Name: "test", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "storageSize")
}

// === US-13.2: workspace.defaultResources ===

func TestCreateWorkspace_DefaultResources_Applied(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultResources.cpu":              "1000m",
		"workspace.defaultResources.memory":           "1Gi",
		"workspace.defaultResources.ephemeralStorage": "2Gi",
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Resources != nil &&
			ws.Spec.Resources.CPU == "1000m" &&
			ws.Spec.Resources.Memory == "1Gi" &&
			ws.Spec.Resources.EphemeralStorage == "2Gi"
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_NoResourceSettings_NilResources(t *testing.T) {
	f := newDefaultsFixture(t, nil) // no settings service at all
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Resources == nil
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
}

// === US-13.4: workspace.defaultSecurityLevel ===

func TestCreateWorkspace_DefaultSecurityLevel_Applied(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultSecurityLevel": "high",
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.SecurityLevel == "high"
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

// === US-13.5: workspace.defaultStorageClass ===

func TestCreateWorkspace_DefaultStorageClass_Applied(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultStorageClass": "fast-ssd",
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Storage.StorageClassName == "fast-ssd"
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_ExplicitStorageClass_NotOverridden(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultStorageClass": "fast-ssd",
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Storage.StorageClassName == "slow-hdd"
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base", StorageClass: "slow-hdd"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

// === US-13.6: workspace.autoSuspend + TTL ===

func TestCreateWorkspace_AutoSuspend_FromSettings(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.autoSuspend.enabled":            false,
		"workspace.autoSuspend.idleTimeoutMinutes": 30,
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.AutoSuspend != nil &&
			ws.Spec.AutoSuspend.Enabled == false &&
			ws.Spec.AutoSuspend.IdleTimeoutSeconds == 1800
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_TTLDays_ConvertedToSeconds(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.ttlDaysAfterSuspended": 7,
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.TTLSecondsAfterSuspended == 7*86400
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

// === US-13.7: workspace.defaultNetworkAccess ===

func TestCreateWorkspace_DefaultNetworkAccess_Applied(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultNetworkAccess.ingress":       true,
		"workspace.defaultNetworkAccess.egressDomains": []string{"api.openai.com", "api.anthropic.com"},
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.NetworkAccess != nil &&
			ws.Spec.NetworkAccess.Ingress == true &&
			len(ws.Spec.NetworkAccess.Egress) == 2 &&
			ws.Spec.NetworkAccess.Egress[0].Domain == "api.openai.com"
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

// === Unhappy paths: settings service errors ===

func TestCreateWorkspace_SettingsError_GracefulDegradation(t *testing.T) {
	// Settings store that returns errors
	errStore := &errorSettingsStore{}
	var log pkginterfaces.LoggerInterface = lmocks.NewMockLogger()
	errSvc := settings.NewInstanceService(errStore, log)

	f := newFixture(t)
	f.svc.SetInstanceSettings(errSvc)
	ctx := context.Background()

	// Should still create workspace with request values (no defaults applied)
	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Runtime == "base" && ws.Spec.Storage.Size == "1Gi"
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
}

// errorSettingsStore always returns an error.
type errorSettingsStore struct{}

func (s *errorSettingsStore) GetAllInstanceSettings(_ context.Context) (map[string]json.RawMessage, error) {
	return nil, fmt.Errorf("database connection refused")
}
func (s *errorSettingsStore) SetInstanceSetting(_ context.Context, _ string, _ json.RawMessage) error {
	return fmt.Errorf("database connection refused")
}

// === Edge cases ===

func TestCreateWorkspace_DefaultStorageSize_ExceedsMax_Rejected(t *testing.T) {
	// Admin misconfigured: default > max
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultStorageSize": "20Gi",
		"workspace.maxStorageSize":     "10Gi",
	})
	ctx := context.Background()

	req := types.CreateWorkspaceRequest{Name: "test", Runtime: "base"} // no storageSize
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestCreateWorkspace_PartialResources_OnlyCPU(t *testing.T) {
	// Only cpu is set in settings, memory/ephemeral use schema defaults
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultResources.cpu": "2000m",
		// memory and ephemeral will come from schema defaults
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Resources != nil && ws.Spec.Resources.CPU == "2000m"
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_TTLZero_NotSetOnCRD(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.ttlDaysAfterSuspended": 0,
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.TTLSecondsAfterSuspended == 0
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_EmptyEgressDomains_NoNetworkAccess(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultNetworkAccess.ingress":       false,
		"workspace.defaultNetworkAccess.egressDomains": []string{},
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.NetworkAccess == nil
	})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_AutoSuspendTimeout_MinutesToSeconds(t *testing.T) {
	// Verify the minutes→seconds conversion for various values
	tests := []struct {
		name          string
		minutes       int
		expectSeconds int64
	}{
		{"1 minute", 1, 60},
		{"60 minutes", 60, 3600},
		{"1440 minutes (1 day)", 1440, 86400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newDefaultsFixture(t, map[string]any{
				"workspace.autoSuspend.idleTimeoutMinutes": tt.minutes,
			})
			ctx := context.Background()

			f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
				return ws.Spec.AutoSuspend != nil &&
					ws.Spec.AutoSuspend.IdleTimeoutSeconds == tt.expectSeconds
			})).Return(crdWorkspace("ws-1", "default", "user1", "1Gi"), nil)
			f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

			req := types.CreateWorkspaceRequest{Name: "test", StorageSize: "1Gi", Runtime: "base"}
			_, err := f.svc.CreateWorkspace(ctx, "user1", req)
			assert.NoError(t, err)
			f.ws.AssertExpectations(t)
		})
	}
}

// === Integration: all defaults applied together ===

func TestCreateWorkspace_AllDefaults_AppliedTogether(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultImage":                       "custom:latest",
		"workspace.defaultStorageSize":                 "5Gi",
		"workspace.maxStorageSize":                     "50Gi",
		"workspace.defaultStorageClass":                "premium",
		"workspace.defaultSecurityLevel":               "high",
		"workspace.defaultResources.cpu":               "2000m",
		"workspace.defaultResources.memory":            "2Gi",
		"workspace.defaultResources.ephemeralStorage":  "4Gi",
		"workspace.autoSuspend.enabled":                true,
		"workspace.autoSuspend.idleTimeoutMinutes":     120,
		"workspace.ttlDaysAfterSuspended":              14,
		"workspace.defaultNetworkAccess.ingress":       true,
		"workspace.defaultNetworkAccess.egressDomains": []string{"api.openai.com"},
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Runtime == "custom:latest" &&
			ws.Spec.Storage.Size == "5Gi" &&
			ws.Spec.Storage.StorageClassName == "premium" &&
			ws.Spec.SecurityLevel == "high" &&
			ws.Spec.Resources != nil &&
			ws.Spec.Resources.CPU == "2000m" &&
			ws.Spec.Resources.Memory == "2Gi" &&
			ws.Spec.Resources.EphemeralStorage == "4Gi" &&
			ws.Spec.AutoSuspend != nil &&
			ws.Spec.AutoSuspend.Enabled == true &&
			ws.Spec.AutoSuspend.IdleTimeoutSeconds == 7200 &&
			ws.Spec.TTLSecondsAfterSuspended == 14*86400 &&
			ws.Spec.NetworkAccess != nil &&
			ws.Spec.NetworkAccess.Ingress == true &&
			len(ws.Spec.NetworkAccess.Egress) == 1
	})).Return(crdWorkspace("ws-1", "default", "user1", "5Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	// Request with NO optional fields — all should come from settings
	req := types.CreateWorkspaceRequest{Name: "full-defaults-test"}
	result, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	f.ws.AssertExpectations(t)
}

func TestCreateWorkspace_ExplicitValues_OverrideAllDefaults(t *testing.T) {
	f := newDefaultsFixture(t, map[string]any{
		"workspace.defaultImage":        "default:latest",
		"workspace.defaultStorageSize":  "1Gi",
		"workspace.maxStorageSize":      "50Gi",
		"workspace.defaultStorageClass": "slow",
	})
	ctx := context.Background()

	f.ws.On("Create", mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.Runtime == "python:3.11" &&
			ws.Spec.Storage.Size == "10Gi" &&
			ws.Spec.Storage.StorageClassName == "fast"
	})).Return(crdWorkspace("ws-1", "default", "user1", "10Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{
		Name:         "explicit",
		Runtime:      "python:3.11",
		StorageSize:  "10Gi",
		StorageClass: "fast",
	}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}
