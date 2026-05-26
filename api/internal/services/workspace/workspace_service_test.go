package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	imocks "github.com/lenaxia/llmsafespace/api/internal/mocks"
	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// fixture wires up all centralized mocks and a real Service under test.
type fixture struct {
	svc     *Service
	k8s     *kmocks.MockKubernetesClient
	v1iface *kmocks.MockLLMSafespaceV1Interface
	ws      *kmocks.MockWorkspaceInterface
	db      *imocks.MockDatabaseService
	cache   *imocks.MockCacheService
	metrics *imocks.MockMetricsService
	log     *lmocks.MockLogger
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	log := lmocks.NewMockLogger()
	log.On("Info", mock.Anything, mock.Anything).Maybe()
	log.On("Debug", mock.Anything, mock.Anything).Maybe()
	log.On("Warn", mock.Anything, mock.Anything).Maybe()
	log.On("Error", mock.Anything, mock.Anything, mock.Anything).Maybe()
	log.On("With", mock.Anything).Return(log).Maybe()
	log.On("Sync").Return(nil).Maybe()

	k8s := kmocks.NewMockKubernetesClient()
	v1i := kmocks.NewMockLLMSafespaceV1Interface()
	ws := kmocks.NewMockWorkspaceInterface()
	db := &imocks.MockDatabaseService{}
	cache := &imocks.MockCacheService{}
	met := &imocks.MockMetricsService{}

	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	k8s.On("LlmsafespaceV1").Return(v1i)
	v1i.On("Workspaces", "default").Return(ws)

	svc, err := New(log, k8s, db, cache, met, &Config{Namespace: "default"})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return &fixture{svc: svc, k8s: k8s, v1iface: v1i, ws: ws, db: db, cache: cache, metrics: met, log: log}
}

func crdWorkspace(name, ns, userID, storageSize string) *v1.Workspace {
	return &v1.Workspace{
		TypeMeta:   metav1.TypeMeta{Kind: "Workspace", APIVersion: "llmsafespace.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: userID},
			Storage: v1.WorkspaceStorageConfig{Size: storageSize},
		},
		Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhasePending},
	}
}

func dbWorkspace(id, userID, name, storageSize string) *types.WorkspaceMetadata {
	return &types.WorkspaceMetadata{
		ID:          id,
		UserID:      userID,
		Name:        name,
		Runtime:     "python:3.10",
		StorageSize: storageSize,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

// ===== New() =====

func TestNew_NilLogger_ReturnsError(t *testing.T) {
	_, err := New(nil, kmocks.NewMockKubernetesClient(), &imocks.MockDatabaseService{}, nil, &imocks.MockMetricsService{}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "logger")
}

func TestNew_NilK8s_ReturnsError(t *testing.T) {
	log := lmocks.NewMockLogger()
	log.On("With", mock.Anything).Return(log).Maybe()
	_, err := New(log, nil, &imocks.MockDatabaseService{}, nil, &imocks.MockMetricsService{}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kubernetes")
}

func TestNew_NilDB_ReturnsError(t *testing.T) {
	log := lmocks.NewMockLogger()
	log.On("With", mock.Anything).Return(log).Maybe()
	_, err := New(log, kmocks.NewMockKubernetesClient(), nil, nil, &imocks.MockMetricsService{}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}

func TestNew_NilConfig_UsesDefaults(t *testing.T) {
	log := lmocks.NewMockLogger()
	log.On("With", mock.Anything).Return(log).Maybe()
	svc, err := New(log, kmocks.NewMockKubernetesClient(), &imocks.MockDatabaseService{}, nil, &imocks.MockMetricsService{}, nil)
	assert.NoError(t, err)
	assert.Equal(t, "default", svc.config.Namespace)
}

// ===== CreateWorkspace =====

func TestCreateWorkspace_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.AnythingOfType("*v1.Workspace")).Return(
		crdWorkspace("ws-1", "default", "user1", "10Gi"), nil,
	)
	f.db.On("CreateWorkspace", ctx, mock.MatchedBy(func(m *types.WorkspaceMetadata) bool {
		return m.UserID == "user1" && m.StorageSize == "10Gi"
	})).Return(nil)

	req := types.CreateWorkspaceRequest{
		Name:        "my-workspace",
		Runtime:     "python:3.10",
		StorageSize: "10Gi",
	}
	result, err := f.svc.CreateWorkspace(ctx, "user1", req)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "user1", result.UserID)
	assert.Equal(t, "10Gi", result.StorageSize)
	f.ws.AssertExpectations(t)
	f.db.AssertExpectations(t)
}

func TestCreateWorkspace_EmptyName_FailsValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	req := types.CreateWorkspaceRequest{StorageSize: "10Gi"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation_error")
	f.ws.AssertNotCalled(t, "Create")
}

func TestCreateWorkspace_EmptyStorageSize_FailsValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	req := types.CreateWorkspaceRequest{Name: "my-workspace"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation_error")
	f.ws.AssertNotCalled(t, "Create")
}

func TestCreateWorkspace_K8sCreateFails_ReturnsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.Anything).Return((*v1.Workspace)(nil), errors.New("k8s unavailable"))

	req := types.CreateWorkspaceRequest{Name: "my-workspace", StorageSize: "10Gi"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_creation_failed")
	f.db.AssertNotCalled(t, "CreateWorkspace")
}

func TestCreateWorkspace_DBCreateFails_CleansUpK8s(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.Anything).Return(crdWorkspace("ws-x", "default", "user1", "10Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(errors.New("db write failed"))
	f.ws.On("Delete", "ws-x", mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "my-workspace", StorageSize: "10Gi"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "metadata_creation_failed")
	f.ws.AssertCalled(t, "Delete", "ws-x", mock.Anything)
}

// ===== GetWorkspace =====

func TestGetWorkspace_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", "ws-1", mock.Anything).Return(crdWorkspace("ws-1", "default", "user1", "10Gi"), nil)

	result, err := f.svc.GetWorkspace(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	assert.Equal(t, "ws-1", result.ID)
	assert.Equal(t, "user1", result.UserID)
}

func TestGetWorkspace_NotFound_ReturnsNotFound(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "missing").Return((*types.WorkspaceMetadata)(nil), nil)

	_, err := f.svc.GetWorkspace(ctx, "user1", "missing")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not_found")
}

func TestGetWorkspace_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	_, err := f.svc.GetWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestGetWorkspace_DBError_ReturnsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return((*types.WorkspaceMetadata)(nil), errors.New("db down"))

	_, err := f.svc.GetWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_retrieval_failed")
}

// ===== ListWorkspaces =====

func TestListWorkspaces_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now()

	metas := []*types.WorkspaceMetadata{
		{ID: "ws-1", UserID: "user1", Name: "ws1", StorageSize: "10Gi", CreatedAt: now},
		{ID: "ws-2", UserID: "user1", Name: "ws2", StorageSize: "5Gi", CreatedAt: now.Add(-time.Hour)},
	}
	f.db.On("ListWorkspaces", ctx, "user1", 10, 0).Return(metas, &types.PaginationMetadata{Total: 2, Limit: 10}, nil)

	result, err := f.svc.ListWorkspaces(ctx, "user1", types.ListOptions{Limit: 10, Offset: 0})

	assert.NoError(t, err)
	assert.Len(t, result.Items, 2)
	assert.Equal(t, "ws-1", result.Items[0].ID)
	assert.Equal(t, 2, result.Pagination.Total)
}

func TestListWorkspaces_Empty_ReturnsEmptyList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("ListWorkspaces", ctx, "user1", 10, 0).Return([]*types.WorkspaceMetadata{}, &types.PaginationMetadata{Total: 0, Limit: 10}, nil)

	result, err := f.svc.ListWorkspaces(ctx, "user1", types.ListOptions{Limit: 10, Offset: 0})

	assert.NoError(t, err)
	assert.Empty(t, result.Items)
}

func TestListWorkspaces_DBFails_ReturnsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("ListWorkspaces", ctx, "user1", 10, 0).Return(
		([]*types.WorkspaceMetadata)(nil), (*types.PaginationMetadata)(nil), errors.New("db down"),
	)

	_, err := f.svc.ListWorkspaces(ctx, "user1", types.ListOptions{Limit: 10, Offset: 0})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_list_failed")
}

// ===== DeleteWorkspace =====

func TestDeleteWorkspace_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Delete", "ws-1", mock.Anything).Return(nil)
	done := make(chan struct{})
	f.db.On("MarkWorkspaceDeleted", ctx, "ws-1").Run(func(_ mock.Arguments) { close(done) })

	err := f.svc.DeleteWorkspace(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for MarkWorkspaceDeleted")
	}
	f.db.AssertExpectations(t)
}

func TestDeleteWorkspace_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	err := f.svc.DeleteWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
	f.ws.AssertNotCalled(t, "Delete")
}

func TestDeleteWorkspace_NotFound_ReturnsNotFound(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return((*types.WorkspaceMetadata)(nil), nil)

	err := f.svc.DeleteWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not_found")
}

// ===== SuspendWorkspace =====

func TestSuspendWorkspace_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	activeCrd.Status.Phase = v1.WorkspacePhaseActive
	f.ws.On("Get", "ws-1", mock.Anything).Return(activeCrd, nil)
	f.ws.On("UpdateStatus", mock.AnythingOfType("*v1.Workspace")).Return(activeCrd, nil)

	err := f.svc.SuspendWorkspace(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestSuspendWorkspace_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	err := f.svc.SuspendWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

// ===== ResumeWorkspace =====

func TestResumeWorkspace_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	suspendedCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	suspendedCrd.Status.Phase = v1.WorkspacePhaseSuspended
	f.ws.On("Get", "ws-1", mock.Anything).Return(suspendedCrd, nil)
	f.ws.On("UpdateStatus", mock.AnythingOfType("*v1.Workspace")).Return(suspendedCrd, nil)

	err := f.svc.ResumeWorkspace(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestResumeWorkspace_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	err := f.svc.ResumeWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

// ===== GetWorkspaceStatus =====

func TestGetWorkspaceStatus_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	activeCrd.Status.Phase = v1.WorkspacePhaseActive
	activeCrd.Status.PVCName = "workspace-ws-1"
	activeCrd.Status.ActiveSessions = 2
	f.ws.On("Get", "ws-1", mock.Anything).Return(activeCrd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	assert.Equal(t, "Active", result.Phase)
	assert.Equal(t, "workspace-ws-1", result.PVCName)
	assert.Equal(t, 2, result.ActiveSessions)
}

func TestGetWorkspaceStatus_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	_, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

// ===== SetCredentials =====

func TestSetCredentials_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	f.ws.On("Get", "ws-1", mock.Anything).Return(activeCrd, nil)

	fakeClient := k8sfake.NewSimpleClientset()
	f.k8s.On("Clientset").Return(fakeClient)

	req := types.SetCredentialsRequest{
		Provider: "openai",
		Config:   []byte(`{"apiKey":"sk-test"}`),
	}

	err := f.svc.SetCredentials(ctx, "user1", "ws-1", req)
	assert.NoError(t, err)

	secret, getErr := fakeClient.CoreV1().Secrets("default").Get(ctx, "workspace-creds-ws-1", metav1.GetOptions{})
	assert.NoError(t, getErr)
	assert.Equal(t, []byte(`{"apiKey":"sk-test"}`), secret.Data["provider-config"])
}

func TestSetCredentials_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	req := types.SetCredentialsRequest{Provider: "openai", Config: []byte(`{}`)}
	err := f.svc.SetCredentials(ctx, "user1", "ws-1", req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestSetCredentials_UpdatesExistingSecret(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	f.ws.On("Get", "ws-1", mock.Anything).Return(activeCrd, nil)

	fakeClient := k8sfake.NewSimpleClientset()
	f.k8s.On("Clientset").Return(fakeClient)

	req1 := types.SetCredentialsRequest{Provider: "openai", Config: []byte(`{"apiKey":"sk-old"}`)}
	assert.NoError(t, f.svc.SetCredentials(ctx, "user1", "ws-1", req1))

	req2 := types.SetCredentialsRequest{Provider: "openai", Config: []byte(`{"apiKey":"sk-new"}`)}
	assert.NoError(t, f.svc.SetCredentials(ctx, "user1", "ws-1", req2))

	secret, _ := fakeClient.CoreV1().Secrets("default").Get(ctx, "workspace-creds-ws-1", metav1.GetOptions{})
	assert.Equal(t, []byte(`{"apiKey":"sk-new"}`), secret.Data["provider-config"])
}

// ===== DeleteCredentials =====

func TestDeleteCredentials_HappyPath_NotFound_IsOK(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	fakeClient := k8sfake.NewSimpleClientset()
	f.k8s.On("Clientset").Return(fakeClient)

	err := f.svc.DeleteCredentials(ctx, "user1", "ws-1")
	assert.NoError(t, err)
}

func TestDeleteCredentials_HappyPath_ExistingSecret(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	f.ws.On("Get", "ws-1", mock.Anything).Return(activeCrd, nil)
	fakeClient := k8sfake.NewSimpleClientset()
	f.k8s.On("Clientset").Return(fakeClient)

	req := types.SetCredentialsRequest{Provider: "openai", Config: []byte(`{"apiKey":"sk-test"}`)}
	assert.NoError(t, f.svc.SetCredentials(ctx, "user1", "ws-1", req))
	assert.NoError(t, f.svc.DeleteCredentials(ctx, "user1", "ws-1"))

	_, err := fakeClient.CoreV1().Secrets("default").Get(ctx, "workspace-creds-ws-1", metav1.GetOptions{})
	assert.Error(t, err)
}

func TestDeleteCredentials_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	err := f.svc.DeleteCredentials(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

// ===== Start / Stop =====

func TestStart_Stop_NoError(t *testing.T) {
	f := newFixture(t)
	assert.NoError(t, f.svc.Start())
	assert.NoError(t, f.svc.Stop())
}

// ===== SuspendWorkspace unhappy paths =====

func TestSuspendWorkspace_K8sGetFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", "ws-1", mock.Anything).Return((*v1.Workspace)(nil), errors.New("k8s unavailable"))

	err := f.svc.SuspendWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_get_failed")
	f.ws.AssertNotCalled(t, "UpdateStatus")
}

func TestSuspendWorkspace_K8sUpdateFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	activeCrd.Status.Phase = v1.WorkspacePhaseActive
	f.ws.On("Get", "ws-1", mock.Anything).Return(activeCrd, nil)
	f.ws.On("UpdateStatus", mock.AnythingOfType("*v1.Workspace")).Return((*v1.Workspace)(nil), errors.New("k8s update failed"))

	err := f.svc.SuspendWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_suspend_failed")
}

// ===== ResumeWorkspace unhappy paths =====

func TestResumeWorkspace_K8sGetFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", "ws-1", mock.Anything).Return((*v1.Workspace)(nil), errors.New("k8s unavailable"))

	err := f.svc.ResumeWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_get_failed")
	f.ws.AssertNotCalled(t, "UpdateStatus")
}

func TestResumeWorkspace_K8sUpdateFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	suspendedCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	suspendedCrd.Status.Phase = v1.WorkspacePhaseSuspended
	f.ws.On("Get", "ws-1", mock.Anything).Return(suspendedCrd, nil)
	f.ws.On("UpdateStatus", mock.AnythingOfType("*v1.Workspace")).Return((*v1.Workspace)(nil), errors.New("k8s update failed"))

	err := f.svc.ResumeWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_resume_failed")
}

// ===== GetWorkspaceStatus unhappy paths =====

func TestGetWorkspaceStatus_K8sGetFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", "ws-1", mock.Anything).Return((*v1.Workspace)(nil), errors.New("k8s unavailable"))

	_, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_get_failed")
}

// ===========================================================================
// E2E tests: Suspend/Resume phase validation (GAP-7 fix verification)
// ===========================================================================

func TestE2E_SuspendWorkspace_OnlyActiveAllowed(t *testing.T) {
	tests := []struct {
		name    string
		phase   v1.WorkspacePhase
		wantErr bool
	}{
		{"Active_allowed", v1.WorkspacePhaseActive, false},
		{"Resuming_rejected", v1.WorkspacePhaseResuming, true},
		{"Suspended_rejected", v1.WorkspacePhaseSuspended, true},
		{"Pending_rejected", v1.WorkspacePhasePending, true},
		{"Terminating_rejected", v1.WorkspacePhaseTerminating, true},
		{"Terminated_rejected", v1.WorkspacePhaseTerminated, true},
		{"Failed_rejected", v1.WorkspacePhaseFailed, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()

			f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)

			crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
			crd.Status.Phase = tt.phase
			f.ws.On("Get", "ws-1", mock.Anything).Return(crd, nil)
			if !tt.wantErr {
				f.ws.On("UpdateStatus", mock.AnythingOfType("*v1.Workspace")).Return(crd, nil)
			}

			err := f.svc.SuspendWorkspace(ctx, "user1", "ws-1")

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestE2E_ResumeWorkspace_OnlySuspendedAllowed(t *testing.T) {
	tests := []struct {
		name    string
		phase   v1.WorkspacePhase
		wantErr bool
	}{
		{"Suspended_allowed", v1.WorkspacePhaseSuspended, false},
		{"Active_rejected", v1.WorkspacePhaseActive, true},
		{"Resuming_rejected", v1.WorkspacePhaseResuming, true},
		{"Pending_rejected", v1.WorkspacePhasePending, true},
		{"Terminating_rejected", v1.WorkspacePhaseTerminating, true},
		{"Terminated_rejected", v1.WorkspacePhaseTerminated, true},
		{"Failed_rejected", v1.WorkspacePhaseFailed, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()

			f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)

			crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
			crd.Status.Phase = tt.phase
			f.ws.On("Get", "ws-1", mock.Anything).Return(crd, nil)
			if !tt.wantErr {
				f.ws.On("UpdateStatus", mock.AnythingOfType("*v1.Workspace")).Return(crd, nil)
			}

			err := f.svc.ResumeWorkspace(ctx, "user1", "ws-1")

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestE2E_CreateWorkspace_SetsOwnerAndStorageInCRD(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.AnythingOfType("*v1.Workspace")).Return(
		crdWorkspace("ws-new", "default", "user1", "10Gi"), nil,
	)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{
		Name:         "e2e-workspace",
		Runtime:      "python:3.11",
		StorageSize:  "10Gi",
		StorageClass: "fast-ssd",
	}

	result, err := f.svc.CreateWorkspace(ctx, "user1", req)
	require.NoError(t, err)
	assert.Equal(t, "user1", result.UserID)
	assert.Equal(t, "10Gi", result.StorageSize)

	f.ws.AssertCalled(t, "Create", mock.MatchedBy(func(crd *v1.Workspace) bool {
		return crd.Spec.Owner.UserID == "user1" &&
			crd.Spec.Storage.Size == "10Gi" &&
			crd.Spec.Storage.StorageClassName == "fast-ssd" &&
			crd.Spec.Runtime == "python:3.11"
	}))
}
