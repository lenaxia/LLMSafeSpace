package workspace

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	imocks "github.com/lenaxia/llmsafespace/api/internal/mocks"
	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type ensureFixture struct {
	svc        *Service
	k8s        *kmocks.MockKubernetesClient
	v1iface    *kmocks.MockLLMSafespaceV1Interface
	ws         *kmocks.MockWorkspaceInterface
	sb         *kmocks.MockSandboxInterface
	db         *imocks.MockDatabaseService
	cache      *imocks.MockCacheService
	metrics    *imocks.MockMetricsService
	sandboxSvc *imocks.MockSandboxService
	log        *lmocks.MockLogger
	clientset  *k8sfake.Clientset
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
	sb := kmocks.NewMockSandboxInterface()
	db := &imocks.MockDatabaseService{}
	cache := &imocks.MockCacheService{}
	met := &imocks.MockMetricsService{}
	sandboxSvc := &imocks.MockSandboxService{}

	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	k8s.On("LlmsafespaceV1").Return(v1i)
	v1i.On("Workspaces", "default").Return(ws)
	v1i.On("Sandboxes", "default").Return(sb)

	clientset := k8sfake.NewSimpleClientset()
	k8s.On("Clientset").Return(clientset)

	svc, err := New(log, k8s, db, cache, met, &Config{Namespace: "default"})
	require.NoError(t, err)
	svc.SetSandboxService(sandboxSvc)

	return &ensureFixture{
		svc: svc, k8s: k8s, v1iface: v1i, ws: ws, sb: sb,
		db: db, cache: cache, metrics: met, sandboxSvc: sandboxSvc,
		log: log, clientset: clientset,
	}
}

func (f *ensureFixture) seedSecret(sandboxID, password string) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sandbox-pw-" + sandboxID, Namespace: "default"},
		Data:       map[string][]byte{"password": []byte(password)},
	}
	_, _ = f.clientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
}

// startSessionServer starts a test HTTP server that responds to POST /session.
// Returns the server, host IP, and port. Caller must defer server.Close().
func startSessionServer(t *testing.T, sessionID string) (*httptest.Server, string, int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/session", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"id": sessionID})
	}))
	host, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return server, host, port
}

// ===== EnsureSession =====

func TestEnsureSession_ActiveWorkspace_RunningSandbox(t *testing.T) {
	f := newEnsureFixture(t)
	ctx := context.Background()

	server, host, port := startSessionServer(t, "sess-123")
	defer server.Close()
	f.svc.config.OpencodePort = port

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", "ws-1", mock.Anything).Return(&v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", Namespace: "default"},
		Spec:       v1.WorkspaceSpec{DefaultRuntime: "base"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
	}, nil)
	f.sb.On("List", mock.Anything).Return(&v1.SandboxList{
		Items: []v1.Sandbox{{
			ObjectMeta: metav1.ObjectMeta{Name: "sb-1", Labels: map[string]string{"user-id": "user1"}},
			Status:     v1.SandboxStatus{Phase: "Running", PodIP: host},
		}},
	}, nil)
	f.sb.On("Get", "sb-1", mock.Anything).Return(&v1.Sandbox{
		Status: v1.SandboxStatus{Phase: "Running", PodIP: host},
	}, nil)
	f.seedSecret("sb-1", "testpass")

	resp, err := f.svc.EnsureSession(ctx, "user1", "ws-1")

	require.NoError(t, err)
	assert.Equal(t, "sb-1", resp.SandboxID)
	assert.Equal(t, "Running", resp.SandboxPhase)
	assert.Equal(t, "sess-123", resp.SessionID)
	assert.False(t, resp.Resumed)
}

func TestEnsureSession_SuspendedWorkspace_ResumesAndCreates(t *testing.T) {
	f := newEnsureFixture(t)
	ctx := context.Background()

	server, host, port := startSessionServer(t, "sess-456")
	defer server.Close()
	f.svc.config.OpencodePort = port

	f.db.On("GetWorkspace", ctx, "ws-2").Return(dbWorkspace("ws-2", "user1", "my-ws", "10Gi"), nil)
	// Workspace is Suspended
	f.ws.On("Get", "ws-2", mock.Anything).Return(&v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-2", Namespace: "default"},
		Spec:       v1.WorkspaceSpec{DefaultRuntime: "python:3.11"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseSuspended},
	}, nil)
	f.ws.On("UpdateStatus", mock.Anything).Return(&v1.Workspace{
		Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseResuming},
	}, nil)

	// No existing sandboxes
	f.sb.On("List", mock.Anything).Return(&v1.SandboxList{Items: []v1.Sandbox{}}, nil)
	f.sandboxSvc.On("CreateSandbox", ctx, mock.MatchedBy(func(req *types.CreateSandboxRequest) bool {
		return req.Runtime == "python:3.11" && req.WorkspaceRef == "ws-2"
	})).Return(&types.Sandbox{ID: "sb-new", Status: types.SandboxStatus{Phase: "Pending"}}, nil)

	f.sb.On("Get", "sb-new", mock.Anything).Return(&v1.Sandbox{
		Status: v1.SandboxStatus{Phase: "Running", PodIP: host},
	}, nil)
	f.seedSecret("sb-new", "pw123")

	resp, err := f.svc.EnsureSession(ctx, "user1", "ws-2")

	require.NoError(t, err)
	assert.Equal(t, "sb-new", resp.SandboxID)
	assert.Equal(t, "sess-456", resp.SessionID)
	assert.True(t, resp.Resumed)
}

func TestEnsureSession_TerminatedWorkspace_ReturnsError(t *testing.T) {
	f := newEnsureFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-dead").Return(dbWorkspace("ws-dead", "user1", "dead", "10Gi"), nil)
	f.ws.On("Get", "ws-dead", mock.Anything).Return(&v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-dead", Namespace: "default"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseTerminated},
	}, nil)

	_, err := f.svc.EnsureSession(ctx, "user1", "ws-dead")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not usable")
}

func TestEnsureSession_FailedWorkspace_ReturnsError(t *testing.T) {
	f := newEnsureFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-fail").Return(dbWorkspace("ws-fail", "user1", "fail", "10Gi"), nil)
	f.ws.On("Get", "ws-fail", mock.Anything).Return(&v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-fail", Namespace: "default"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseFailed},
	}, nil)

	_, err := f.svc.EnsureSession(ctx, "user1", "ws-fail")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not usable")
}

func TestEnsureSession_FailedSandbox_CreatesNew(t *testing.T) {
	f := newEnsureFixture(t)
	ctx := context.Background()

	server, host, port := startSessionServer(t, "sess-new")
	defer server.Close()
	f.svc.config.OpencodePort = port

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "ws", "10Gi"), nil)
	f.ws.On("Get", "ws-1", mock.Anything).Return(&v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", Namespace: "default"},
		Spec:       v1.WorkspaceSpec{DefaultRuntime: "base"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
	}, nil)
	// Only a Failed sandbox exists — should be skipped
	f.sb.On("List", mock.Anything).Return(&v1.SandboxList{
		Items: []v1.Sandbox{{
			ObjectMeta: metav1.ObjectMeta{Name: "sb-old", Labels: map[string]string{"user-id": "user1"}},
			Status:     v1.SandboxStatus{Phase: "Failed"},
		}},
	}, nil)
	f.sandboxSvc.On("CreateSandbox", ctx, mock.Anything).Return(
		&types.Sandbox{ID: "sb-fresh", Status: types.SandboxStatus{Phase: "Pending"}}, nil,
	)
	f.sb.On("Get", "sb-fresh", mock.Anything).Return(&v1.Sandbox{
		Status: v1.SandboxStatus{Phase: "Running", PodIP: host},
	}, nil)
	f.seedSecret("sb-fresh", "pw")

	resp, err := f.svc.EnsureSession(ctx, "user1", "ws-1")

	require.NoError(t, err)
	assert.Equal(t, "sb-fresh", resp.SandboxID)
	assert.Equal(t, "sess-new", resp.SessionID)
	f.sandboxSvc.AssertCalled(t, "CreateSandbox", ctx, mock.Anything)
}

func TestEnsureSession_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newEnsureFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "ws", "10Gi"), nil)

	_, err := f.svc.EnsureSession(ctx, "user1", "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

// ===== CreateWorkspace auto-sandbox =====

func TestCreateWorkspace_AutoCreatesSandbox(t *testing.T) {
	f := newEnsureFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.Anything).Return(crdWorkspace("ws-new", "default", "user1", "10Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)
	f.sandboxSvc.On("CreateSandbox", ctx, mock.MatchedBy(func(req *types.CreateSandboxRequest) bool {
		return req.UserID == "user1" && req.Runtime == "python:3.10"
	})).Return(&types.Sandbox{ID: "sb-auto"}, nil)

	req := types.CreateWorkspaceRequest{Name: "test", Runtime: "python:3.10", StorageSize: "10Gi"}
	result, err := f.svc.CreateWorkspace(ctx, "user1", req)

	require.NoError(t, err)
	assert.Equal(t, "sb-auto", result.SandboxID)
}

func TestCreateWorkspace_SandboxFailure_StillReturnsWorkspace(t *testing.T) {
	f := newEnsureFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.Anything).Return(crdWorkspace("ws-new", "default", "user1", "10Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(nil)
	f.sandboxSvc.On("CreateSandbox", ctx, mock.Anything).Return((*types.Sandbox)(nil), assert.AnError)

	req := types.CreateWorkspaceRequest{Name: "test", Runtime: "base", StorageSize: "10Gi"}
	result, err := f.svc.CreateWorkspace(ctx, "user1", req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.SandboxID)
}

func TestEnsureSession_CreatingSandbox_WaitsForRunning(t *testing.T) {
	f := newEnsureFixture(t)
	ctx := context.Background()

	server, host, port := startSessionServer(t, "sess-wait")
	defer server.Close()
	f.svc.config.OpencodePort = port

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "ws", "10Gi"), nil)
	f.ws.On("Get", "ws-1", mock.Anything).Return(&v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", Namespace: "default"},
		Spec:       v1.WorkspaceSpec{DefaultRuntime: "base"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
	}, nil)
	// Sandbox exists but is Creating
	f.sb.On("List", mock.Anything).Return(&v1.SandboxList{
		Items: []v1.Sandbox{{
			ObjectMeta: metav1.ObjectMeta{Name: "sb-wait", Labels: map[string]string{"user-id": "user1"}},
			Status:     v1.SandboxStatus{Phase: "Creating"},
		}},
	}, nil)
	// First Get: still Creating. Second Get: Running.
	f.sb.On("Get", "sb-wait", mock.Anything).Return(
		&v1.Sandbox{Status: v1.SandboxStatus{Phase: "Creating"}}, nil,
	).Once()
	f.sb.On("Get", "sb-wait", mock.Anything).Return(
		&v1.Sandbox{Status: v1.SandboxStatus{Phase: "Running", PodIP: host}}, nil,
	)
	f.seedSecret("sb-wait", "pw")

	resp, err := f.svc.EnsureSession(ctx, "user1", "ws-1")

	require.NoError(t, err)
	assert.Equal(t, "sb-wait", resp.SandboxID)
	assert.Equal(t, "sess-wait", resp.SessionID)
}
