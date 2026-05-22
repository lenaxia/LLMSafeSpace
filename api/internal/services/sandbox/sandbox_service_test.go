package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	sb      *kmocks.MockSandboxInterface
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
	sb := kmocks.NewMockSandboxInterface()
	db := &imocks.MockDatabaseService{}
	cache := &imocks.MockCacheService{}
	met := &imocks.MockMetricsService{}

	// Metrics lifecycle always called around CreateSandbox
	met.On("Start").Return(nil).Maybe()
	met.On("Stop").Return(nil).Maybe()
	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	k8s.On("LlmsafespaceV1").Return(v1i)
	v1i.On("Sandboxes", "default").Return(sb)

	svc, err := New(log, k8s, db, cache, met, &Config{
		Namespace:      "default",
		DefaultTimeout: 300,
		MaxSandboxes:   100,
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return &fixture{svc: svc, k8s: k8s, v1iface: v1i, sb: sb, db: db, cache: cache, metrics: met, log: log}
}

func crdSandbox(name, ns, runtime string) *v1.Sandbox {
	return &v1.Sandbox{
		TypeMeta:   metav1.TypeMeta{Kind: "Sandbox", APIVersion: "llmsafespace.dev/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1.SandboxSpec{Runtime: runtime, SecurityLevel: "standard", Timeout: 300},
		Status:     v1.SandboxStatus{Phase: "Pending"},
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
	assert.Equal(t, 300, svc.config.DefaultTimeout)
	assert.Equal(t, 100, svc.config.MaxSandboxes)
}

// ===== CreateSandbox =====

func TestCreateSandbox_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)
	f.db.On("GetUser", ctx, "user1").Return(&types.User{ID: "user1"}, nil)
	f.db.On("CheckPermission", "user1", "sandbox", "", "create").Return(true, nil)
	f.db.On("CreateSandbox", ctx, mock.MatchedBy(func(m *types.SandboxMetadata) bool {
		return m.ID == "sb-1" && m.UserID == "user1" && m.Runtime == "python:3.10"
	})).Return(nil)
	f.sb.On("Create", mock.AnythingOfType("*v1.Sandbox")).Return(crdSandbox("sb-1", "default", "python:3.10"), nil)
	f.metrics.On("RecordSandboxCreation", "python:3.10", "user1").Return()

	req := &types.CreateSandboxRequest{Runtime: "python:3.10", SecurityLevel: "standard", Timeout: 300, UserID: "user1"}
	result, err := f.svc.CreateSandbox(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "sb-1", result.Name)
	assert.Equal(t, "python:3.10", result.Spec.Runtime)
	f.sb.AssertExpectations(t)
	f.db.AssertExpectations(t)
	f.metrics.AssertExpectations(t)
}

func TestCreateSandbox_EmptyRuntime_FailsValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)

	_, err := f.svc.CreateSandbox(ctx, &types.CreateSandboxRequest{Runtime: "", UserID: "user1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation_error")
	f.sb.AssertNotCalled(t, "Create")
	f.db.AssertNotCalled(t, "CreateSandbox")
}

func TestCreateSandbox_InvalidSecurityLevel_FailsValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)

	_, err := f.svc.CreateSandbox(ctx, &types.CreateSandboxRequest{Runtime: "python:3.10", SecurityLevel: "nuclear", UserID: "user1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation_error")
}

func TestCreateSandbox_UserNotFound_ReturnsNotFound(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)
	f.db.On("GetUser", ctx, "nobody").Return((*types.User)(nil), nil)

	_, err := f.svc.CreateSandbox(ctx, &types.CreateSandboxRequest{Runtime: "python:3.10", UserID: "nobody"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not_found")
}

func TestCreateSandbox_GetUserError_ReturnsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)
	f.db.On("GetUser", ctx, "user1").Return((*types.User)(nil), errors.New("db timeout"))

	_, err := f.svc.CreateSandbox(ctx, &types.CreateSandboxRequest{Runtime: "python:3.10", UserID: "user1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user_retrieval_failed")
}

func TestCreateSandbox_PermissionDenied_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)
	f.db.On("GetUser", ctx, "user1").Return(&types.User{ID: "user1"}, nil)
	f.db.On("CheckPermission", "user1", "sandbox", "", "create").Return(false, nil)

	_, err := f.svc.CreateSandbox(ctx, &types.CreateSandboxRequest{Runtime: "python:3.10", UserID: "user1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestCreateSandbox_K8sCreateFails_ReturnsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)
	f.db.On("GetUser", ctx, "user1").Return(&types.User{ID: "user1"}, nil)
	f.db.On("CheckPermission", "user1", "sandbox", "", "create").Return(true, nil)
	f.sb.On("Create", mock.Anything).Return((*v1.Sandbox)(nil), errors.New("k8s unavailable"))

	_, err := f.svc.CreateSandbox(ctx, &types.CreateSandboxRequest{Runtime: "python:3.10", UserID: "user1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox_creation_failed")
	f.db.AssertNotCalled(t, "CreateSandbox")
}

func TestCreateSandbox_DBCreateFails_CleansUpK8s(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)
	f.db.On("GetUser", ctx, "user1").Return(&types.User{ID: "user1"}, nil)
	f.db.On("CheckPermission", "user1", "sandbox", "", "create").Return(true, nil)
	f.sb.On("Create", mock.Anything).Return(crdSandbox("sb-x", "default", "python:3.10"), nil)
	f.db.On("CreateSandbox", ctx, mock.Anything).Return(errors.New("db write failed"))
	f.sb.On("Delete", "sb-x", mock.Anything).Return(nil)

	_, err := f.svc.CreateSandbox(ctx, &types.CreateSandboxRequest{Runtime: "python:3.10", UserID: "user1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "metadata_creation_failed")
	f.sb.AssertCalled(t, "Delete", "sb-x", mock.Anything)
}

func TestCreateSandbox_ZeroTimeout_AppliesDefault(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)
	f.db.On("GetUser", ctx, "user1").Return(&types.User{ID: "user1"}, nil)
	f.db.On("CheckPermission", "user1", "sandbox", "", "create").Return(true, nil)
	f.db.On("CreateSandbox", ctx, mock.Anything).Return(nil)
	f.metrics.On("RecordSandboxCreation", mock.Anything, mock.Anything).Return()

	var capturedTimeout int
	f.sb.On("Create", mock.MatchedBy(func(s *v1.Sandbox) bool {
		capturedTimeout = s.Spec.Timeout
		return true
	})).Return(crdSandbox("sb-t", "default", "python:3.10"), nil)

	_, err := f.svc.CreateSandbox(ctx, &types.CreateSandboxRequest{Runtime: "python:3.10", UserID: "user1", Timeout: 0})
	assert.NoError(t, err)
	assert.Equal(t, 300, capturedTimeout)
}

func TestCreateSandbox_LabelsAndAnnotationsSet(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("Start").Return(nil)
	f.db.On("Stop").Return(nil)
	f.db.On("GetUser", ctx, "user1").Return(&types.User{ID: "user1"}, nil)
	f.db.On("CheckPermission", "user1", "sandbox", "", "create").Return(true, nil)
	f.db.On("CreateSandbox", ctx, mock.Anything).Return(nil)
	f.metrics.On("RecordSandboxCreation", mock.Anything, mock.Anything).Return()

	var captured *v1.Sandbox
	f.sb.On("Create", mock.MatchedBy(func(s *v1.Sandbox) bool {
		captured = s
		return true
	})).Return(crdSandbox("sb-l", "default", "python:3.10"), nil)

	_, err := f.svc.CreateSandbox(ctx, &types.CreateSandboxRequest{Runtime: "python:3.10", UserID: "user1", Timeout: 60})
	assert.NoError(t, err)
	assert.Equal(t, "sb-", captured.GenerateName)
	assert.Equal(t, "user1", captured.Labels["user-id"])
	assert.Equal(t, "python:3.10", captured.Labels["runtime"])
	assert.NotEmpty(t, captured.Annotations["llmsafespace.dev/created-at"])
}

// ===== GetSandbox =====

func TestGetSandbox_FoundInDefaultNamespace(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.sb.On("Get", "sb-1", mock.Anything).Return(crdSandbox("sb-1", "default", "python:3.10"), nil)

	result, err := f.svc.GetSandbox(ctx, "sb-1")
	assert.NoError(t, err)
	assert.Equal(t, "sb-1", result.Name)
	assert.Equal(t, "python:3.10", result.Spec.Runtime)
}

func TestGetSandbox_FallbackToAllNamespaces(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.sb.On("Get", "sb-2", mock.Anything).Return((*v1.Sandbox)(nil), errors.New("not found"))

	allNsSb := kmocks.NewMockSandboxInterface()
	f.v1iface.On("Sandboxes", "").Return(allNsSb)
	crd := crdSandbox("sb-2", "other-ns", "nodejs:18")
	allNsSb.On("List", mock.Anything).Return(&v1.SandboxList{Items: []v1.Sandbox{*crd}}, nil)

	result, err := f.svc.GetSandbox(ctx, "sb-2")
	assert.NoError(t, err)
	assert.Equal(t, "sb-2", result.Name)
	assert.Equal(t, "other-ns", result.Namespace)
}

func TestGetSandbox_NotFoundAnywhere_ReturnsSandboxNotFoundError(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.sb.On("Get", "missing", mock.Anything).Return((*v1.Sandbox)(nil), errors.New("not found"))
	allNsSb := kmocks.NewMockSandboxInterface()
	f.v1iface.On("Sandboxes", "").Return(allNsSb)
	allNsSb.On("List", mock.Anything).Return(&v1.SandboxList{Items: []v1.Sandbox{}}, nil)

	_, err := f.svc.GetSandbox(ctx, "missing")
	assert.Error(t, err)
	assert.IsType(t, &types.SandboxNotFoundError{}, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestGetSandbox_ListFails_ReturnsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.sb.On("Get", "sb-1", mock.Anything).Return((*v1.Sandbox)(nil), errors.New("not found"))
	allNsSb := kmocks.NewMockSandboxInterface()
	f.v1iface.On("Sandboxes", "").Return(allNsSb)
	allNsSb.On("List", mock.Anything).Return((*v1.SandboxList)(nil), errors.New("list failed"))

	_, err := f.svc.GetSandbox(ctx, "sb-1")
	assert.Error(t, err)
	assert.NotNil(t, err)
}

// ===== GetSandboxStatus =====

func TestGetSandboxStatus_ReturnsStatus(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	crd := crdSandbox("sb-1", "default", "python:3.10")
	crd.Status.Phase = "Running"
	crd.Status.Resources = &v1.ResourceStatus{CPUUsage: "100m", MemoryUsage: "256Mi"}
	f.sb.On("Get", "sb-1", mock.Anything).Return(crd, nil)

	status, err := f.svc.GetSandboxStatus(ctx, "sb-1")
	assert.NoError(t, err)
	assert.Equal(t, "Running", status.Phase)
	assert.Equal(t, "100m", status.Resources.CPUUsage)
	assert.Equal(t, "256Mi", status.Resources.MemoryUsage)
}

func TestGetSandboxStatus_NotFound_ReturnsAPIError(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.sb.On("Get", "missing", mock.Anything).Return((*v1.Sandbox)(nil), errors.New("not found"))
	allNsSb := kmocks.NewMockSandboxInterface()
	f.v1iface.On("Sandboxes", "").Return(allNsSb)
	allNsSb.On("List", mock.Anything).Return(&v1.SandboxList{Items: []v1.Sandbox{}}, nil)

	_, err := f.svc.GetSandboxStatus(ctx, "missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not_found")
}

// ===== TerminateSandbox =====

func TestTerminateSandbox_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.WithValue(context.Background(), "userID", "user1")

	f.sb.On("Get", "sb-1", mock.Anything).Return(crdSandbox("sb-1", "default", "python:3.10"), nil)
	f.db.On("CheckResourceOwnership", "user1", "sandbox", "sb-1").Return(true, nil)
	f.sb.On("Delete", "sb-1", mock.Anything).Return(nil)
	f.db.On("DeleteSandbox", ctx, "sb-1").Return(nil)
	f.metrics.On("RecordSandboxTermination", "python:3.10", "user_requested").Return()

	assert.NoError(t, f.svc.TerminateSandbox(ctx, "sb-1"))
	f.sb.AssertExpectations(t)
	f.db.AssertExpectations(t)
	f.metrics.AssertExpectations(t)
}

func TestTerminateSandbox_NotFound_ReturnsNotFound(t *testing.T) {
	f := newFixture(t)
	ctx := context.WithValue(context.Background(), "userID", "user1")

	f.sb.On("Get", "missing", mock.Anything).Return((*v1.Sandbox)(nil), errors.New("not found"))
	allNsSb := kmocks.NewMockSandboxInterface()
	f.v1iface.On("Sandboxes", "").Return(allNsSb)
	allNsSb.On("List", mock.Anything).Return(&v1.SandboxList{Items: []v1.Sandbox{}}, nil)

	err := f.svc.TerminateSandbox(ctx, "missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not_found")
}

func TestTerminateSandbox_NoUserInContext_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background() // no userID

	f.sb.On("Get", "sb-1", mock.Anything).Return(crdSandbox("sb-1", "default", "python:3.10"), nil)

	err := f.svc.TerminateSandbox(ctx, "sb-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
	f.sb.AssertNotCalled(t, "Delete")
}

func TestTerminateSandbox_NotOwner_NoPermission_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.WithValue(context.Background(), "userID", "intruder")

	f.sb.On("Get", "sb-1", mock.Anything).Return(crdSandbox("sb-1", "default", "python:3.10"), nil)
	f.db.On("CheckResourceOwnership", "intruder", "sandbox", "sb-1").Return(false, nil)
	f.db.On("CheckPermission", "intruder", "sandbox", "sb-1", "delete").Return(false, nil)

	err := f.svc.TerminateSandbox(ctx, "sb-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
	f.sb.AssertNotCalled(t, "Delete")
}

func TestTerminateSandbox_NotOwner_HasPermission_Succeeds(t *testing.T) {
	f := newFixture(t)
	ctx := context.WithValue(context.Background(), "userID", "admin")

	f.sb.On("Get", "sb-1", mock.Anything).Return(crdSandbox("sb-1", "default", "python:3.10"), nil)
	f.db.On("CheckResourceOwnership", "admin", "sandbox", "sb-1").Return(false, nil)
	f.db.On("CheckPermission", "admin", "sandbox", "sb-1", "delete").Return(true, nil)
	f.sb.On("Delete", "sb-1", mock.Anything).Return(nil)
	f.db.On("DeleteSandbox", ctx, "sb-1").Return(nil)
	f.metrics.On("RecordSandboxTermination", "python:3.10", "user_requested").Return()

	assert.NoError(t, f.svc.TerminateSandbox(ctx, "sb-1"))
}

func TestTerminateSandbox_K8sDeleteFails_ReturnsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.WithValue(context.Background(), "userID", "user1")

	f.sb.On("Get", "sb-1", mock.Anything).Return(crdSandbox("sb-1", "default", "python:3.10"), nil)
	f.db.On("CheckResourceOwnership", "user1", "sandbox", "sb-1").Return(true, nil)
	f.sb.On("Delete", "sb-1", mock.Anything).Return(errors.New("k8s error"))

	err := f.svc.TerminateSandbox(ctx, "sb-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox_termination_failed")
	f.db.AssertNotCalled(t, "DeleteSandbox")
}

func TestTerminateSandbox_MetadataDeleteFails_ReturnsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.WithValue(context.Background(), "userID", "user1")

	f.sb.On("Get", "sb-1", mock.Anything).Return(crdSandbox("sb-1", "default", "python:3.10"), nil)
	f.db.On("CheckResourceOwnership", "user1", "sandbox", "sb-1").Return(true, nil)
	f.sb.On("Delete", "sb-1", mock.Anything).Return(nil)
	f.db.On("DeleteSandbox", ctx, "sb-1").Return(errors.New("db error"))
	f.metrics.On("RecordSandboxTermination", mock.Anything, mock.Anything).Return()

	err := f.svc.TerminateSandbox(ctx, "sb-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "metadata_deletion_failed")
}

// ===== ListSandboxes =====

func TestListSandboxes_ReturnsSortedNewestFirst(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now()

	metas := []*types.SandboxMetadata{
		{ID: "sb-1", UserID: "u1", Runtime: "python:3.10", CreatedAt: now, Status: "Running"},
		{ID: "sb-2", UserID: "u1", Runtime: "nodejs:18", CreatedAt: now.Add(-time.Hour), Status: "Pending"},
	}
	f.db.On("ListSandboxes", ctx, "u1", 10, 0).Return(metas, &types.PaginationMetadata{Total: 2, Limit: 10}, nil)
	f.sb.On("Get", "sb-1", mock.Anything).Return(crdSandbox("sb-1", "default", "python:3.10"), nil)
	f.sb.On("Get", "sb-2", mock.Anything).Return(crdSandbox("sb-2", "default", "nodejs:18"), nil)

	results, err := f.svc.ListSandboxes(ctx, "u1", 10, 0)
	assert.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, "sb-1", results[0]["id"])
	assert.Equal(t, "sb-2", results[1]["id"])
}

func TestListSandboxes_K8sGetFails_StillReturnsRow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	metas := []*types.SandboxMetadata{
		{ID: "sb-1", UserID: "u1", Runtime: "python:3.10", CreatedAt: time.Now(), Status: "Running"},
	}
	f.db.On("ListSandboxes", ctx, "u1", 10, 0).Return(metas, &types.PaginationMetadata{Total: 1}, nil)
	f.sb.On("Get", "sb-1", mock.Anything).Return((*v1.Sandbox)(nil), errors.New("not found"))

	results, err := f.svc.ListSandboxes(ctx, "u1", 10, 0)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "sb-1", results[0]["id"])
}

func TestListSandboxes_DBFails_ReturnsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("ListSandboxes", ctx, "u1", 10, 0).Return(
		([]*types.SandboxMetadata)(nil), (*types.PaginationMetadata)(nil), errors.New("db down"),
	)
	_, err := f.svc.ListSandboxes(ctx, "u1", 10, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox_list_failed")
}

func TestListSandboxes_ErrNotFound_ReturnsNotFound(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("ListSandboxes", ctx, "u1", 10, 0).Return(
		([]*types.SandboxMetadata)(nil), (*types.PaginationMetadata)(nil), types.ErrNotFound,
	)
	_, err := f.svc.ListSandboxes(ctx, "u1", 10, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not_found")
}

func TestListSandboxes_ErrPermissionDenied_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("ListSandboxes", ctx, "u1", 10, 0).Return(
		([]*types.SandboxMetadata)(nil), (*types.PaginationMetadata)(nil), types.ErrPermissionDenied,
	)
	_, err := f.svc.ListSandboxes(ctx, "u1", 10, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestListSandboxes_PaginationAttachedToAllRows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now()

	metas := []*types.SandboxMetadata{
		{ID: "sb-1", UserID: "u1", Runtime: "python:3.10", CreatedAt: now},
		{ID: "sb-2", UserID: "u1", Runtime: "nodejs:18", CreatedAt: now.Add(-time.Minute)},
	}
	pag := &types.PaginationMetadata{Total: 2, Limit: 10, Offset: 0}
	f.db.On("ListSandboxes", ctx, "u1", 10, 0).Return(metas, pag, nil)
	f.sb.On("Get", mock.Anything, mock.Anything).Return((*v1.Sandbox)(nil), errors.New("skip"))

	results, err := f.svc.ListSandboxes(ctx, "u1", 10, 0)
	assert.NoError(t, err)
	for _, row := range results {
		p, ok := row["pagination"].(*types.PaginationMetadata)
		assert.True(t, ok, "pagination must be *types.PaginationMetadata")
		assert.Equal(t, 2, p.Total)
	}
}

// ===== Start / Stop =====

func TestStart_Stop_NoError(t *testing.T) {
	f := newFixture(t)
	assert.NoError(t, f.svc.Start())
	assert.NoError(t, f.svc.Stop())
}

// ===== type conversion =====

func TestConvertCRDToAPI_MapsAllFields(t *testing.T) {
	crd := &v1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-1", Namespace: "ns1"},
		Spec: v1.SandboxSpec{
			Runtime:       "python:3.10",
			SecurityLevel: "standard",
			Timeout:       300,
			Resources:     &v1.ResourceRequirements{CPU: "500m", Memory: "512Mi", EphemeralStorage: "1Gi"},
			NetworkAccess: &v1.NetworkAccess{
				Ingress: true,
				Egress:  []v1.EgressRule{{Domain: "api.example.com", Ports: []v1.PortRule{{Port: 443, Protocol: "TCP"}}}},
			},
			Filesystem: &v1.FilesystemConfig{ReadOnlyRoot: true, WritablePaths: []string{"/tmp"}},
			Storage:    &v1.StorageConfig{Persistent: true, VolumeSize: "10Gi"},
			ProfileRef: &v1.ProfileReference{Name: "default-profile", Namespace: "ns1"},
		},
		Status: v1.SandboxStatus{
			Phase:     "Running",
			PodName:   "sb-1-pod",
			Resources: &v1.ResourceStatus{CPUUsage: "100m", MemoryUsage: "256Mi"},
		},
	}

	api := convertCRDToAPI(crd)

	assert.Equal(t, "sb-1", api.Name)
	assert.Equal(t, "ns1", api.Namespace)
	assert.Equal(t, "python:3.10", api.Spec.Runtime)
	assert.Equal(t, "standard", api.Spec.SecurityLevel)
	assert.Equal(t, 300, api.Spec.Timeout)
	assert.Equal(t, "500m", api.Spec.Resources.CPU)
	assert.Equal(t, "512Mi", api.Spec.Resources.Memory)
	assert.Equal(t, "1Gi", api.Spec.Resources.EphemeralStorage)
	assert.True(t, api.Spec.NetworkAccess.Ingress)
	assert.Equal(t, "api.example.com", api.Spec.NetworkAccess.Egress[0].Domain)
	assert.Equal(t, 443, api.Spec.NetworkAccess.Egress[0].Ports[0].Port)
	assert.Equal(t, "TCP", api.Spec.NetworkAccess.Egress[0].Ports[0].Protocol)
	assert.True(t, api.Spec.Filesystem.ReadOnlyRoot)
	assert.Equal(t, []string{"/tmp"}, api.Spec.Filesystem.WritablePaths)
	assert.True(t, api.Spec.Storage.Persistent)
	assert.Equal(t, "10Gi", api.Spec.Storage.VolumeSize)
	assert.Equal(t, "default-profile", api.Spec.ProfileRef.Name)
	assert.Equal(t, "Running", api.Status.Phase)
	assert.Equal(t, "sb-1-pod", api.Status.PodName)
	assert.Equal(t, "100m", api.Status.Resources.CPUUsage)
}

func TestConvertCRDToAPI_NilInput_ReturnsNil(t *testing.T) {
	assert.Nil(t, convertCRDToAPI(nil))
}

func TestConvertCRDToAPI_NilOptionalFields_NoNilPanic(t *testing.T) {
	crd := &v1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-1"},
		Spec:       v1.SandboxSpec{Runtime: "python:3.10"},
	}
	api := convertCRDToAPI(crd)
	assert.Nil(t, api.Spec.Resources)
	assert.Nil(t, api.Spec.NetworkAccess)
	assert.Nil(t, api.Spec.Filesystem)
	assert.Nil(t, api.Spec.Storage)
	assert.Nil(t, api.Spec.ProfileRef)
	assert.Nil(t, api.Status.Resources)
}

func TestBuildCRDFromRequest_SetsAllFields(t *testing.T) {
	req := &types.CreateSandboxRequest{
		Runtime:       "python:3.10",
		SecurityLevel: "standard",
		Timeout:       120,
		UserID:        "user42",
		Resources:     &types.ResourceRequirements{CPU: "1", Memory: "1Gi"},
		NetworkAccess: &types.NetworkAccess{
			Ingress: true,
			Egress:  []types.EgressRule{{Domain: "pypi.org", Ports: []types.PortRule{{Port: 443, Protocol: "TCP"}}}},
		},
	}

	crd := buildCRDFromRequest(req, "testns")

	assert.Equal(t, "sb-", crd.GenerateName)
	assert.Equal(t, "testns", crd.Namespace)
	assert.Equal(t, "user42", crd.Labels["user-id"])
	assert.Equal(t, "python:3.10", crd.Labels["runtime"])
	assert.Equal(t, "llmsafespace", crd.Labels["app"])
	assert.Equal(t, "user42", crd.Annotations["llmsafespace.dev/created-by"])
	assert.NotEmpty(t, crd.Annotations["llmsafespace.dev/created-at"])
	assert.Equal(t, "python:3.10", crd.Spec.Runtime)
	assert.Equal(t, "standard", crd.Spec.SecurityLevel)
	assert.Equal(t, 120, crd.Spec.Timeout)
	assert.Equal(t, "1", crd.Spec.Resources.CPU)
	assert.Equal(t, "1Gi", crd.Spec.Resources.Memory)
	assert.True(t, crd.Spec.NetworkAccess.Ingress)
	assert.Equal(t, "pypi.org", crd.Spec.NetworkAccess.Egress[0].Domain)
}

func TestBuildCRDFromRequest_NilResources_NoNilPanic(t *testing.T) {
	req := &types.CreateSandboxRequest{Runtime: "python:3.10", UserID: "u1"}
	crd := buildCRDFromRequest(req, "ns")
	assert.Nil(t, crd.Spec.Resources)
	assert.Nil(t, crd.Spec.NetworkAccess)
}
