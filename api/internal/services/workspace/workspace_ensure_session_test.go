package workspace

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	imocks "github.com/lenaxia/llmsafespace/api/internal/mocks"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type ensureFixture struct {
	svc       *Service
	ws        *kmocks.MockWorkspaceInterface
	db        *imocks.MockDatabaseService
	clientset *k8sfake.Clientset
}

func newEnsureFixture(t *testing.T) *ensureFixture {
	t.Helper()

	log := lmocks.NewMockLogger()
	log.On("Info", mock.Anything, mock.Anything).Maybe()
	log.On("Debug", mock.Anything, mock.Anything).Maybe()
	log.On("Warn", mock.Anything, mock.Anything).Maybe()
	log.On("Error", mock.Anything, mock.Anything, mock.Anything).Maybe()
	log.On("With", mock.Anything).Return(log).Maybe()

	k8s := kmocks.NewMockKubernetesClient()
	v1i := kmocks.NewMockLLMSafespaceV1Interface()
	ws := kmocks.NewMockWorkspaceInterface()
	db := &imocks.MockDatabaseService{}
	cache := &imocks.MockCacheService{}
	met := &imocks.MockMetricsService{}

	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	k8s.On("LlmsafespaceV1").Return(v1i)
	v1i.On("Workspaces", "default").Return(ws)

	clientset := k8sfake.NewSimpleClientset()
	k8s.On("Clientset").Return(clientset)

	svc, err := New(log, k8s, db, cache, met, &Config{Namespace: "default", OpencodePort: 4096})
	require.NoError(t, err)

	return &ensureFixture{svc: svc, ws: ws, db: db, clientset: clientset}
}

func TestEnsureSession_TerminatedWorkspace_ReturnsError(t *testing.T) {
	f := newEnsureFixture(t)

	crd := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", Namespace: "default"},
		Spec:       v1.WorkspaceSpec{Runtime: "python:3.11", Owner: v1.WorkspaceOwner{UserID: "user-1"}},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseTerminated},
	}
	f.ws.On("Get", "ws-1", mock.Anything).Return(crd, nil)
	f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(&types.WorkspaceMetadata{UserID: "user-1"}, nil)

	_, err := f.svc.EnsureSession(context.Background(), "user-1", "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not usable")
}

func TestEnsureSession_FailedWorkspace_ReturnsError(t *testing.T) {
	f := newEnsureFixture(t)

	crd := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", Namespace: "default"},
		Spec:       v1.WorkspaceSpec{Runtime: "python:3.11", Owner: v1.WorkspaceOwner{UserID: "user-1"}},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseFailed},
	}
	f.ws.On("Get", "ws-1", mock.Anything).Return(crd, nil)
	f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(&types.WorkspaceMetadata{UserID: "user-1"}, nil)

	_, err := f.svc.EnsureSession(context.Background(), "user-1", "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not usable")
}

func TestEnsureSession_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newEnsureFixture(t)

	f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(&types.WorkspaceMetadata{UserID: "other-user"}, nil)

	_, err := f.svc.EnsureSession(context.Background(), "user-1", "ws-1")
	assert.Error(t, err)
}

func TestEnsureSession_WorkspaceNotFound_ReturnsError(t *testing.T) {
	f := newEnsureFixture(t)

	f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(nil, fmt.Errorf("not found"))

	_, err := f.svc.EnsureSession(context.Background(), "user-1", "ws-1")
	assert.Error(t, err)
}
