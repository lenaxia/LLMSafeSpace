// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
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

	k8s.On("LlmsafespaceV1").Return(v1i, nil)
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

// fixtureWithFakeClientset extends fixture with an in-memory K8s
// Clientset so tests can observe Secret writes (workspace-secrets-<id>)
// without standing up a real apiserver. Used by the
// refreshEphemeralSecrets tests added in worklog 0120.
type fixtureWithFakeClientset struct {
	*fixture
	fakeCS *k8sfake.Clientset
}

func newFixtureWithFakeClientset(t *testing.T) *fixtureWithFakeClientset {
	t.Helper()
	f := newFixture(t)
	cs := k8sfake.NewSimpleClientset()
	// Stub Clientset() so EnsureSecretsManifest can reach the fake.
	f.k8s.On("Clientset").Return(k8s.Interface(cs))
	return &fixtureWithFakeClientset{fixture: f, fakeCS: cs}
}

// workspaceCtxWithSession is a thin alias for ContextWithSessionID
// for readability in test code. Kept as a function (not a constant)
// so future test-only setup (e.g. tracing IDs) can be added in one
// place without touching every call site.
func workspaceCtxWithSession(ctx context.Context, sessionID string) context.Context {
	return ContextWithSessionID(ctx, sessionID)
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
	assert.NotNil(t, svc)
}

// ===== RestartWorkspace (Epic 21 Change A) =====
//
// RestartWorkspace bumps spec.restartGeneration and writes the spec via
// Update (not UpdateStatus, which the controller uses to flip phase).
// The controller's handleFailed and handleActive both observe the bump
// and walk back to Pending (or delete the running pod for Active).

func TestRestartWorkspace_FromFailed_BumpsRestartGeneration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	failedCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	failedCrd.Status.Phase = v1.WorkspacePhaseFailed
	failedCrd.Spec.RestartGeneration = 2
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(failedCrd, nil)
	f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.RestartGeneration == 3
	})).Return(failedCrd, nil)

	err := f.svc.RestartWorkspace(ctx, "user1", "ws-1")
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
	// MUST NOT touch status — that's the controller's job after the spec bump.
	f.ws.AssertNotCalled(t, "UpdateStatus")
}

func TestRestartWorkspace_FromActive_BumpsRestartGeneration(t *testing.T) {
	// Restart from any non-terminal phase is allowed; this lets users
	// recover from "stuck" Active workspaces (where the agent is hung
	// but the controller hasn't given up yet) without waiting for the
	// transient-failure budget to exhaust.
	f := newFixture(t)
	ctx := context.Background()

	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	activeCrd.Status.Phase = v1.WorkspacePhaseActive
	activeCrd.Spec.RestartGeneration = 0
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(activeCrd, nil)
	f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.RestartGeneration == 1
	})).Return(activeCrd, nil)

	err := f.svc.RestartWorkspace(ctx, "user1", "ws-1")
	assert.NoError(t, err)
	f.ws.AssertExpectations(t)
}

func TestRestartWorkspace_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	err := f.svc.RestartWorkspace(ctx, "user1", "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
	f.ws.AssertNotCalled(t, "Update")
}

func TestRestartWorkspace_FromTerminating_Rejected(t *testing.T) {
	// Terminating/Terminated are genuinely terminal; restarting them
	// would race with finalizer logic. Reject explicitly with conflict.
	for _, phase := range []v1.WorkspacePhase{v1.WorkspacePhaseTerminating, v1.WorkspacePhaseTerminated} {
		t.Run(string(phase), func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()

			crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
			crd.Status.Phase = phase
			f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
			f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)

			err := f.svc.RestartWorkspace(ctx, "user1", "ws-1")
			assert.Error(t, err)
			f.ws.AssertNotCalled(t, "Update")
		})
	}
}

func TestRestartWorkspace_K8sGetFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return((*v1.Workspace)(nil), errors.New("boom"))

	err := f.svc.RestartWorkspace(ctx, "user1", "ws-1")
	assert.Error(t, err)
	f.ws.AssertNotCalled(t, "Update")
}

func TestRestartWorkspace_K8sUpdateFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	failedCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	failedCrd.Status.Phase = v1.WorkspacePhaseFailed
	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(failedCrd, nil)
	f.ws.On("Update", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return((*v1.Workspace)(nil), errors.New("etcd unavailable"))

	err := f.svc.RestartWorkspace(ctx, "user1", "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_restart_failed")
}

// ===== refreshEphemeralSecrets (worklog 0120) =====
//
// Centralizes the workspace-secrets-<id> refresh logic for both
// ActivateWorkspace (resume from Suspended) and RestartWorkspace
// (bump restartGeneration). Pre-0119, RestartWorkspace had no refresh
// at all, so users lost SSH keys and other bound secrets on restart.
//
// Behavior contract under test:
//   - secretInjector nil  → no-op (default test wiring)
//   - sessionID missing   → no-op + Warn (admin/script restart path)
//   - bindings empty ("[]") → no-op (preserve existing K8s Secret)
//   - bindings non-empty   → write workspace-secrets-<id> via the
//     fake clientset; verify name and payload
//   - PrepareSecretsForInjection error → Warn, no Secret write,
//     lifecycle action proceeds (caller error path tested separately)

// fakeSecretInjector lets tests stub PrepareSecretsForInjection
// without pulling in the full secret service.
type fakeSecretInjector struct {
	prepare func(ctx context.Context, userID, sessionID, workspaceID string) ([]byte, error)
	calls   int
}

func (f *fakeSecretInjector) PrepareSecretsForInjection(ctx context.Context, userID, sessionID, workspaceID string) ([]byte, error) {
	f.calls++
	if f.prepare == nil {
		return []byte("[]"), nil
	}
	return f.prepare(ctx, userID, sessionID, workspaceID)
}

func TestRefreshEphemeralSecrets_NilInjector_NoOp(t *testing.T) {
	// Service created without SetSecretInjector — refresh must be a
	// pure no-op. No clientset call, no log entry beyond the implicit
	// caller. This is the production default in tests that don't care
	// about the secrets path.
	f := newFixture(t)
	ctx := workspaceCtxWithSession(context.Background(), "sess-1")

	// Clientset is not stubbed — calling it would panic, proving no-op.
	f.svc.refreshEphemeralSecrets(ctx, "user1", "ws-1")
}

func TestRefreshEphemeralSecrets_NoSessionID_FallsBackToAdminCredentials(t *testing.T) {
	// Without a user sessionID (e.g. API-key auth, controller reconcile),
	// refreshEphemeralSecrets must NOT skip entirely — it falls back to
	// seedEphemeralSecrets (sessionID="") so that admin platform credentials
	// (server-side KEK, no session required) are still injected.
	//
	// Regression: the old behavior was to skip with a Warn log, leaving
	// ActivateWorkspace callers without platform credentials when the activate
	// request was made with an API key instead of a JWT session.
	f := newFixtureWithFakeClientset(t)
	payload := []byte(`[{"type":"llm-provider","name":"thekao","metadata":{},"plaintext":"sk-..."}]`)
	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, sessionID, _ string) ([]byte, error) {
			// The fallback path calls PrepareSecretsForInjection with sessionID=""
			assert.Equal(t, "", sessionID, "fallback must pass sessionID='' (admin KEK path)")
			return payload, nil
		},
	}
	f.svc.SetSecretInjector(inj)
	ctx := context.Background() // no sessionID

	f.svc.refreshEphemeralSecrets(ctx, "user1", "ws-1")

	// Injector must have been called once (via seedEphemeralSecrets fallback)
	assert.Equal(t, 1, inj.calls, "injector must be called once via seedEphemeralSecrets fallback")

	// workspace-secrets must have been written
	got, err := f.fakeCS.CoreV1().Secrets("default").Get(ctx, "workspace-secrets-ws-1", metav1.GetOptions{})
	require.NoError(t, err, "workspace-secrets must be written via admin-credential fallback")
	assert.Equal(t, payload, got.Data["secrets.json"])
}

func TestRefreshEphemeralSecrets_EmptyBindings_NoWrite(t *testing.T) {
	// PrepareSecretsForInjection returns "[]" when no bindings exist.
	// Writing that would clobber any pre-existing K8s Secret. The
	// helper must short-circuit on len <= 2 to preserve prior state.
	f := newFixtureWithFakeClientset(t)
	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return []byte("[]"), nil
		},
	}
	f.svc.SetSecretInjector(inj)
	ctx := workspaceCtxWithSession(context.Background(), "sess-1")

	f.svc.refreshEphemeralSecrets(ctx, "user1", "ws-1")

	assert.Equal(t, 1, inj.calls)
	// No Secret object was created.
	secrets, err := f.fakeCS.CoreV1().Secrets("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, secrets.Items, "no manifest write expected for empty bindings")
}

func TestRefreshEphemeralSecrets_NonEmptyBindings_WritesManifest(t *testing.T) {
	// The happy path: real bindings produce a real K8s Secret named
	// `workspace-secrets-<id>` carrying secrets.json. This is the
	// regression test for worklog 0120 — RestartWorkspace must
	// produce this Secret before the controller rebuilds the pod.
	f := newFixtureWithFakeClientset(t)
	payload := []byte(`[{"type":"ssh-key","name":"github","metadata":{},"plaintext":"-----BEGIN..."}]`)
	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return payload, nil
		},
	}
	f.svc.SetSecretInjector(inj)
	ctx := workspaceCtxWithSession(context.Background(), "sess-1")

	f.svc.refreshEphemeralSecrets(ctx, "user1", "ws-1")

	got, err := f.fakeCS.CoreV1().Secrets("default").Get(ctx, "workspace-secrets-ws-1", metav1.GetOptions{})
	require.NoError(t, err, "workspace-secrets-<id> must exist after refresh")
	assert.Equal(t, payload, got.Data["secrets.json"],
		"secrets.json payload must round-trip exactly through the manifest write")
	assert.Equal(t, "true", got.Labels["llmsafespace.dev/ephemeral"],
		"ephemeral marker label is required for cleanup logic in workspace deletion")
	assert.Equal(t, "ws-1", got.Labels["llmsafespace.dev/workspace"])
}

func TestRefreshEphemeralSecrets_PrepareFails_SkipsWriteCleanly(t *testing.T) {
	// PrepareSecretsForInjection failure must not propagate to the
	// caller's lifecycle action. The helper logs Warn and returns;
	// the existing K8s Secret (if any) is preserved.
	f := newFixtureWithFakeClientset(t)
	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return nil, errors.New("DEK unwrap failed")
		},
	}
	f.svc.SetSecretInjector(inj)
	ctx := workspaceCtxWithSession(context.Background(), "sess-1")

	f.svc.refreshEphemeralSecrets(ctx, "user1", "ws-1")

	secrets, err := f.fakeCS.CoreV1().Secrets("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, secrets.Items,
		"Prepare failure must NOT result in a partial/empty Secret being written")
}

func TestRestartWorkspace_RefreshesEphemeralSecrets(t *testing.T) {
	// Integration: the full RestartWorkspace path invokes
	// refreshEphemeralSecrets BEFORE bumping restartGeneration. This
	// is the regression test for the worklog 0120 bug: a user's SSH
	// key disappeared after restart because the Secret was never
	// re-emitted before the controller rebuilt the pod.
	f := newFixtureWithFakeClientset(t)
	payload := []byte(`[{"type":"ssh-key","name":"github","metadata":{},"plaintext":"k"}]`)
	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return payload, nil
		},
	}
	f.svc.SetSecretInjector(inj)

	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	activeCrd.Status.Phase = v1.WorkspacePhaseActive
	f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(activeCrd, nil)
	f.ws.On("Update", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return(activeCrd, nil)

	ctx := workspaceCtxWithSession(context.Background(), "sess-1")
	err := f.svc.RestartWorkspace(ctx, "user1", "ws-1")
	require.NoError(t, err)

	got, err := f.fakeCS.CoreV1().Secrets("default").Get(ctx, "workspace-secrets-ws-1", metav1.GetOptions{})
	require.NoError(t, err, "RestartWorkspace must produce workspace-secrets-<id>")
	assert.Equal(t, payload, got.Data["secrets.json"])
	assert.Equal(t, 1, inj.calls)
}

func TestRestartWorkspace_RefreshFailureDoesNotBlockBump(t *testing.T) {
	// If the refresh fails (e.g. DEK unwrap error), the workspace
	// restart must still proceed — the user wants the pod recreated
	// regardless. The pre-existing K8s Secret (if any) carries forward
	// to the new pod. This trades secret freshness for liveness, which
	// is the right trade-off: the user can re-bind to refresh; they
	// cannot easily un-stick a workspace that refused to restart.
	f := newFixtureWithFakeClientset(t)
	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return nil, errors.New("DEK unavailable")
		},
	}
	f.svc.SetSecretInjector(inj)

	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	activeCrd.Status.Phase = v1.WorkspacePhaseActive
	activeCrd.Spec.RestartGeneration = 5
	f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(activeCrd, nil)
	f.ws.On("Update", mock.Anything, mock.MatchedBy(func(ws *v1.Workspace) bool {
		return ws.Spec.RestartGeneration == 6
	})).Return(activeCrd, nil)

	ctx := workspaceCtxWithSession(context.Background(), "sess-1")
	err := f.svc.RestartWorkspace(ctx, "user1", "ws-1")
	assert.NoError(t, err, "refresh failure must not propagate")
	f.ws.AssertExpectations(t)
}

// ===== CreateWorkspace =====

func TestCreateWorkspace_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return(
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

	f.ws.On("Create", mock.Anything, mock.Anything).Return((*v1.Workspace)(nil), errors.New("k8s unavailable"))

	req := types.CreateWorkspaceRequest{Name: "my-workspace", StorageSize: "10Gi"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_creation_failed")
	f.db.AssertNotCalled(t, "CreateWorkspace")
}

func TestCreateWorkspace_DBCreateFails_CleansUpK8s(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.Anything, mock.Anything).Return(crdWorkspace("ws-x", "default", "user1", "10Gi"), nil)
	f.db.On("CreateWorkspace", ctx, mock.Anything).Return(errors.New("db write failed"))
	f.ws.On("Delete", mock.Anything, "ws-x", mock.Anything).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "my-workspace", StorageSize: "10Gi"}
	_, err := f.svc.CreateWorkspace(ctx, "user1", req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "metadata_creation_failed")
	f.ws.AssertCalled(t, "Delete", mock.Anything, "ws-x", mock.Anything)
}

// ===== seedEphemeralSecrets =====
//
// seedEphemeralSecrets is the CreateWorkspace-only variant of
// refreshEphemeralSecrets. Key differences from refreshEphemeralSecrets:
//   - Does NOT require sessionID in context (admin credentials use server-side KEK)
//   - Uses the same fail-open semantics (Warn + return, never fails the caller)
//   - Called with sessionID="" — correct at creation time
//
// Tests mirror the refreshEphemeralSecrets suite but without the sessionID
// precondition.

func TestSeedEphemeralSecrets_NilInjector_NoOp(t *testing.T) {
	// Service created without SetSecretInjector — seedEphemeralSecrets must be
	// a pure no-op. No clientset call, no log entry. Same guarantee as refreshEphemeralSecrets.
	f := newFixture(t)
	ctx := context.Background() // no sessionID needed

	// Clientset is not stubbed — calling it would panic, proving no-op.
	f.svc.seedEphemeralSecrets(ctx, "user1", "ws-1")
}

func TestSeedEphemeralSecrets_NonEmptyBindings_WritesManifest(t *testing.T) {
	// Happy path: admin platform credentials produce a real K8s Secret named
	// `workspace-secrets-<id>`. This is the regression test for the bug where
	// CreateWorkspace seeded DB bindings but never wrote the K8s Secret, causing
	// the pod init container to find no credentials on first boot.
	// Note: no sessionID in context — this is the distinguishing property vs
	// refreshEphemeralSecrets which would skip (and Warn) without a sessionID.
	f := newFixtureWithFakeClientset(t)
	payload := []byte(`[{"type":"llm-provider","name":"openai","metadata":{},"plaintext":"sk-..."}]`)
	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, sessionID, _ string) ([]byte, error) {
			// Verify seedEphemeralSecrets passes sessionID="" as documented.
			assert.Equal(t, "", sessionID, "seedEphemeralSecrets must pass sessionID='' (admin KEK path)")
			return payload, nil
		},
	}
	f.svc.SetSecretInjector(inj)
	ctx := context.Background() // no sessionID in context

	f.svc.seedEphemeralSecrets(ctx, "user1", "ws-1")

	got, err := f.fakeCS.CoreV1().Secrets("default").Get(ctx, "workspace-secrets-ws-1", metav1.GetOptions{})
	require.NoError(t, err, "workspace-secrets-<id> must exist after seedEphemeralSecrets")
	assert.Equal(t, payload, got.Data["secrets.json"],
		"secrets.json payload must round-trip exactly through the manifest write")
	assert.Equal(t, "true", got.Labels["llmsafespace.dev/ephemeral"])
	assert.Equal(t, "ws-1", got.Labels["llmsafespace.dev/workspace"])
	assert.Equal(t, 1, inj.calls)
}

func TestSeedEphemeralSecrets_PrepareFails_SkipsWriteCleanly(t *testing.T) {
	// PrepareSecretsForInjection failure must not propagate to CreateWorkspace.
	// The helper logs Warn and returns; the workspace is created successfully
	// (no credentials injected — the pod boots with relay-only access).
	f := newFixtureWithFakeClientset(t)
	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return nil, errors.New("KEK derivation failed")
		},
	}
	f.svc.SetSecretInjector(inj)
	ctx := context.Background()

	f.svc.seedEphemeralSecrets(ctx, "user1", "ws-1") // must not panic or return error

	secrets, err := f.fakeCS.CoreV1().Secrets("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, secrets.Items,
		"Prepare failure must NOT result in a partial/empty Secret being written")
}

func TestSeedEphemeralSecrets_EmptyResult_NoWrite(t *testing.T) {
	// When PrepareSecretsForInjection succeeds but returns empty JSON (no
	// bindings resolved — e.g. credential seeding race or all decrypts failed),
	// seedEphemeralSecrets must NOT write a workspace-secrets Secret.
	// Writing an empty Secret would be worse than writing nothing: the init
	// container would mount it and overwrite any previously correct content.
	f := newFixtureWithFakeClientset(t)
	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return []byte(`[]`), nil // empty binding list — 2 bytes
		},
	}
	f.svc.SetSecretInjector(inj)
	ctx := context.Background()

	f.svc.seedEphemeralSecrets(ctx, "user1", "ws-empty")

	secrets, err := f.fakeCS.CoreV1().Secrets("default").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, secrets.Items,
		"Empty PrepareSecretsForInjection result must NOT write a Secret")
	// injector must have been called exactly once
	assert.Equal(t, 1, inj.calls)
}

// fakeCredentialProvisioner stubs CredentialProvisioner for CreateWorkspace tests.
type fakeCredentialProvisioner struct {
	err   error
	calls int
}

func (f *fakeCredentialProvisioner) SeedWorkspaceCredentials(_ context.Context, _, _ string) error {
	f.calls++
	return f.err
}

func TestCreateWorkspace_SeedsEphemeralSecrets(t *testing.T) {
	// With sessionID in context: refreshEphemeralSecrets is called with the
	// session JTI, injecting the full user credential set on first boot.
	// This is the normal path — the user must be authenticated to create a
	// workspace, so their DEK is always available.
	f := newFixtureWithFakeClientset(t)
	payload := []byte(`[{"type":"llm-provider","name":"thekao","metadata":{},"plaintext":"sk-abc"}]`)

	cp := &fakeCredentialProvisioner{}
	f.svc.SetCredentialProvisioner(cp)

	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, sessionID, _ string) ([]byte, error) {
			assert.Equal(t, "test-jti", sessionID,
				"CreateWorkspace must propagate sessionID from context to PrepareSecretsForInjection")
			return payload, nil
		},
	}
	f.svc.SetSecretInjector(inj)

	f.ws.On("Create", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return(
		crdWorkspace("ws-new", "default", "user1", "5Gi"), nil,
	)
	f.db.On("CreateWorkspace", mock.Anything, mock.MatchedBy(func(m *types.WorkspaceMetadata) bool {
		return m.UserID == "user1"
	})).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "new-workspace", Runtime: "base", StorageSize: "5Gi"}
	ctx := ContextWithSessionID(context.Background(), "test-jti")
	result, err := f.svc.CreateWorkspace(ctx, "user1", req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 1, cp.calls, "SeedWorkspaceCredentials must be called once")

	got, err := f.fakeCS.CoreV1().Secrets("default").Get(context.Background(), "workspace-secrets-"+result.ID, metav1.GetOptions{})
	require.NoError(t, err, "workspace-secrets-<id> must exist after CreateWorkspace")
	assert.Equal(t, payload, got.Data["secrets.json"])
	assert.Equal(t, 1, inj.calls, "PrepareSecretsForInjection must be called exactly once")
}

func TestCreateWorkspace_SeedsEphemeralSecrets_NoSession_FallsBackToSeed(t *testing.T) {
	// Without sessionID (API-key auth, SDK): falls back to seedEphemeralSecrets
	// (admin-only). This is the degraded path — user creds will be injected
	// on next ActivateWorkspace call when a session is available.
	f := newFixtureWithFakeClientset(t)
	payload := []byte(`[{"type":"platform","name":"openai","metadata":{},"plaintext":"sk-proj-abc"}]`)

	cp := &fakeCredentialProvisioner{}
	f.svc.SetCredentialProvisioner(cp)

	inj := &fakeSecretInjector{
		prepare: func(_ context.Context, _, sessionID, _ string) ([]byte, error) {
			assert.Equal(t, "", sessionID,
				"no-session path must call PrepareSecretsForInjection with sessionID=''")
			return payload, nil
		},
	}
	f.svc.SetSecretInjector(inj)

	f.ws.On("Create", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return(
		crdWorkspace("ws-new", "default", "user1", "5Gi"), nil,
	)
	f.db.On("CreateWorkspace", mock.Anything, mock.MatchedBy(func(m *types.WorkspaceMetadata) bool {
		return m.UserID == "user1"
	})).Return(nil)

	req := types.CreateWorkspaceRequest{Name: "new-workspace", Runtime: "base", StorageSize: "5Gi"}
	// context.Background() — no sessionID
	result, err := f.svc.CreateWorkspace(context.Background(), "user1", req)
	require.NoError(t, err)
	require.NotNil(t, result)

	got, err := f.fakeCS.CoreV1().Secrets("default").Get(context.Background(), "workspace-secrets-"+result.ID, metav1.GetOptions{})
	require.NoError(t, err, "workspace-secrets-<id> must exist even without session")
	assert.Equal(t, payload, got.Data["secrets.json"])
}

// ===== GetWorkspace =====

func TestGetWorkspace_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crdWorkspace("ws-1", "default", "user1", "10Gi"), nil)

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
//
// Phase is owned by the Workspace CRD; the DB stores immutable metadata only.
// ListWorkspaces issues one label-scoped CRD list per call and joins phase by
// name. These tests pin that contract.

func TestListWorkspaces_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now()

	metas := []*types.WorkspaceMetadata{
		{ID: "ws-1", UserID: "user1", Name: "ws1", StorageSize: "10Gi", CreatedAt: now},
		{ID: "ws-2", UserID: "user1", Name: "ws2", StorageSize: "5Gi", CreatedAt: now.Add(-time.Hour)},
	}
	f.db.On("ListWorkspaces", ctx, "user1", 10, 0).Return(metas, &types.PaginationMetadata{Total: 2, Limit: 10}, nil)

	crdList := &v1.WorkspaceList{Items: []v1.Workspace{
		{ObjectMeta: metav1.ObjectMeta{Name: "ws-1"}, Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive}},
		{ObjectMeta: metav1.ObjectMeta{Name: "ws-2"}, Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseSuspended}},
	}}
	f.ws.On("List", mock.Anything, metav1.ListOptions{LabelSelector: "user-id=user1"}).Return(crdList, nil)

	result, err := f.svc.ListWorkspaces(ctx, "user1", types.ListOptions{Limit: 10, Offset: 0})

	assert.NoError(t, err)
	assert.Len(t, result.Items, 2)
	assert.Equal(t, "ws-1", result.Items[0].ID)
	assert.Equal(t, "Active", result.Items[0].Phase, "phase comes from the CRD")
	assert.Equal(t, "Suspended", result.Items[1].Phase, "phase comes from the CRD")
	assert.Equal(t, 2, result.Pagination.Total)
}

func TestListWorkspaces_Empty_ReturnsEmptyList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("ListWorkspaces", ctx, "user1", 10, 0).Return([]*types.WorkspaceMetadata{}, &types.PaginationMetadata{Total: 0, Limit: 10}, nil)
	f.ws.On("List", mock.Anything, metav1.ListOptions{LabelSelector: "user-id=user1"}).Return(&v1.WorkspaceList{}, nil)

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

// When the kube-apiserver is unavailable we cannot determine phase. The list
// endpoint must NOT fail the request — it returns the items with empty phase
// so the rest of the dashboard still loads. The platform is unusable in this
// state regardless (every other endpoint also needs k8s) but failing the list
// page would compound the outage.
func TestListWorkspaces_K8sListFails_ReturnsItemsWithEmptyPhase(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now()

	metas := []*types.WorkspaceMetadata{
		{ID: "ws-1", UserID: "user1", Name: "ws1", StorageSize: "10Gi", CreatedAt: now},
	}
	f.db.On("ListWorkspaces", ctx, "user1", 10, 0).Return(metas, &types.PaginationMetadata{Total: 1, Limit: 10}, nil)
	f.ws.On("List", mock.Anything, metav1.ListOptions{LabelSelector: "user-id=user1"}).
		Return((*v1.WorkspaceList)(nil), errors.New("apiserver down"))

	result, err := f.svc.ListWorkspaces(ctx, "user1", types.ListOptions{Limit: 10, Offset: 0})

	assert.NoError(t, err)
	assert.Len(t, result.Items, 1)
	assert.Equal(t, "", result.Items[0].Phase, "no CRD => no phase")
}

// A DB row that has no matching CRD (e.g. mid-deletion) is still returned with
// empty phase rather than dropped from the response.
func TestListWorkspaces_CRDMissing_PhaseEmpty(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now()

	metas := []*types.WorkspaceMetadata{
		{ID: "ws-1", UserID: "user1", Name: "ws1", StorageSize: "10Gi", CreatedAt: now},
	}
	f.db.On("ListWorkspaces", ctx, "user1", 10, 0).Return(metas, &types.PaginationMetadata{Total: 1, Limit: 10}, nil)
	f.ws.On("List", mock.Anything, metav1.ListOptions{LabelSelector: "user-id=user1"}).Return(&v1.WorkspaceList{}, nil)

	result, err := f.svc.ListWorkspaces(ctx, "user1", types.ListOptions{Limit: 10, Offset: 0})

	assert.NoError(t, err)
	assert.Len(t, result.Items, 1)
	assert.Equal(t, "", result.Items[0].Phase)
}

// ===== DeleteWorkspace =====

func TestDeleteWorkspace_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Delete", mock.Anything, "ws-1", mock.Anything).Return(nil)
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
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(activeCrd, nil)
	f.ws.On("UpdateStatus", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return(activeCrd, nil)

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

// ===== ActivateWorkspace (phase transition + credential injection) =====

func TestActivateWorkspace_HappyPath_TransitionsToResuming(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	suspendedCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	suspendedCrd.Status.Phase = v1.WorkspacePhaseSuspended
	staleActivity := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	suspendedCrd.Status.LastActivityAt = &staleActivity

	var captured *v1.Workspace
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(suspendedCrd, nil)
	f.ws.On("UpdateStatus", mock.Anything, mock.AnythingOfType("*v1.Workspace")).
		Run(func(args mock.Arguments) { captured = args.Get(1).(*v1.Workspace) }).
		Return(suspendedCrd, nil)
	// enforceMaxActiveWorkspaces calls List
	f.ws.On("List", mock.Anything, mock.Anything).Return(&v1.WorkspaceList{}, nil)
	// refreshEphemeralSecrets is fire-and-forget; no mock needed

	resp, err := f.svc.ActivateWorkspace(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ws-1", resp.Resumed)
	require.NotNil(t, captured, "UpdateStatus must be called")
	assert.Equal(t, v1.WorkspacePhaseResuming, captured.Status.Phase)
	require.NotNil(t, captured.Status.LastActivityAt, "LastActivityAt must be reset on activate")
	assert.WithinDuration(t, time.Now(), captured.Status.LastActivityAt.Time, 5*time.Second,
		"LastActivityAt must advance to a recent time, was %v", captured.Status.LastActivityAt.Time)
}

func TestActivateWorkspace_WrongOwner_ReturnsForbidden(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "other-user", "my-ws", "10Gi"), nil)

	_, err := f.svc.ActivateWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestActivateWorkspace_K8sGetFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("List", mock.Anything, mock.Anything).Return(&v1.WorkspaceList{}, nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return((*v1.Workspace)(nil), errors.New("k8s unavailable"))

	_, err := f.svc.ActivateWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_get_failed")
	f.ws.AssertNotCalled(t, "UpdateStatus")
}

func TestActivateWorkspace_K8sUpdateFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	suspendedCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	suspendedCrd.Status.Phase = v1.WorkspacePhaseSuspended
	f.ws.On("List", mock.Anything, mock.Anything).Return(&v1.WorkspaceList{}, nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(suspendedCrd, nil)
	f.ws.On("UpdateStatus", mock.Anything, mock.AnythingOfType("*v1.Workspace")).
		Return((*v1.Workspace)(nil), errors.New("k8s update failed"))

	_, err := f.svc.ActivateWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_resume_failed")
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
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(activeCrd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	assert.Equal(t, "Active", result.Phase)
	assert.Equal(t, "workspace-ws-1", result.PVCName)
	assert.Equal(t, 2, result.ActiveSessions)
}

func TestGetWorkspaceStatus_IncludesSessionContextUsed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	activeCrd.Status.Phase = v1.WorkspacePhaseActive
	activeCrd.Status.Sessions = []v1.AgentSessionStatus{
		{ID: "ses_1", Title: "main", Status: "idle", ContextUsed: 42000},
		{ID: "ses_2", Title: "other", Status: "busy", ContextUsed: 99000},
	}
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(activeCrd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	require.Len(t, result.Sessions, 2)
	assert.Equal(t, int64(42000), result.Sessions[0].ContextUsed, "ses_1 ContextUsed threaded to API response")
	assert.Equal(t, int64(99000), result.Sessions[1].ContextUsed, "ses_2 ContextUsed threaded to API response")
}

func TestGetWorkspaceStatus_IncludesContextTotal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	activeCrd.Status.Phase = v1.WorkspacePhaseActive
	activeCrd.Status.ContextUsed = 0
	activeCrd.Status.ContextTotal = 200000
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(activeCrd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	assert.Equal(t, int64(0), result.ContextUsed, "ContextUsed threaded to API response")
	assert.Equal(t, int64(200000), result.ContextTotal, "ContextTotal threaded to API response")
}

func TestGetWorkspaceStatus_ContextTotal_ZeroNotDropped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	activeCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	activeCrd.Status.Phase = v1.WorkspacePhaseActive
	activeCrd.Status.ContextUsed = 0
	activeCrd.Status.ContextTotal = 0
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(activeCrd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)

	raw, jsonErr := json.Marshal(result)
	require.NoError(t, jsonErr)
	assert.Contains(t, string(raw), `"contextUsed":0`, "omitempty removed — zero contextUsed must appear in JSON")
	assert.Contains(t, string(raw), `"contextTotal":0`, "omitempty removed — zero contextTotal must appear in JSON")
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
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return((*v1.Workspace)(nil), errors.New("k8s unavailable"))

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
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(activeCrd, nil)
	f.ws.On("UpdateStatus", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return((*v1.Workspace)(nil), errors.New("k8s update failed"))

	err := f.svc.SuspendWorkspace(ctx, "user1", "ws-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_suspend_failed")
}

// ===== GetWorkspaceStatus unhappy paths =====

func TestGetWorkspaceStatus_K8sGetFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return((*v1.Workspace)(nil), errors.New("k8s unavailable"))

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
			f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
			if !tt.wantErr {
				f.ws.On("UpdateStatus", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return(crd, nil)
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

func TestE2E_ActivateWorkspace_OnlySuspendedOrActiveAllowed(t *testing.T) {
	tests := []struct {
		name    string
		phase   v1.WorkspacePhase
		wantErr bool
	}{
		{"Suspended_allowed", v1.WorkspacePhaseSuspended, false},
		{"Active_idempotent", v1.WorkspacePhaseActive, false},
		{"Resuming_idempotent", v1.WorkspacePhaseResuming, false},
		{"Creating_idempotent", v1.WorkspacePhaseCreating, false},
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
			f.ws.On("List", mock.Anything, mock.Anything).Return(&v1.WorkspaceList{}, nil)

			crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
			crd.Status.Phase = tt.phase
			f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(crd, nil)
			if tt.phase == v1.WorkspacePhaseSuspended {
				f.ws.On("UpdateStatus", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return(crd, nil)
			}

			_, err := f.svc.ActivateWorkspace(ctx, "user1", "ws-1")

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSuspendWorkspace_Idempotent_AlreadySuspended(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	suspendedCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	suspendedCrd.Status.Phase = v1.WorkspacePhaseSuspended
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(suspendedCrd, nil)

	err := f.svc.SuspendWorkspace(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	f.ws.AssertNotCalled(t, "UpdateStatus")
}

func TestSuspendWorkspace_Idempotent_AlreadySuspending(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	suspendingCrd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	suspendingCrd.Status.Phase = v1.WorkspacePhaseSuspending
	f.ws.On("Get", mock.Anything, "ws-1", mock.Anything).Return(suspendingCrd, nil)

	err := f.svc.SuspendWorkspace(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	f.ws.AssertNotCalled(t, "UpdateStatus")
}

func TestE2E_CreateWorkspace_SetsOwnerAndStorageInCRD(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.Anything, mock.AnythingOfType("*v1.Workspace")).Return(
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

	f.ws.AssertCalled(t, "Create", mock.Anything, mock.MatchedBy(func(crd *v1.Workspace) bool {
		return crd.Spec.Owner.UserID == "user1" &&
			crd.Spec.Storage.Size == "10Gi" &&
			crd.Spec.Storage.StorageClassName == "fast-ssd" &&
			crd.Spec.Runtime == "python:3.11"
	}))
}

func TestCredStateFromConditions(t *testing.T) {
	tests := []struct {
		name       string
		conditions []v1.WorkspaceCondition
		expected   types.CredentialStateResult
	}{
		{"valid", []v1.WorkspaceCondition{{Type: v1.WorkspaceConditionCredentialsAvailable, Status: "True", Reason: v1.ReasonCredentialsValid}}, types.CredentialStateResult{Available: true, Reason: v1.ReasonCredentialsValid}},
		{"not found", []v1.WorkspaceCondition{{Type: v1.WorkspaceConditionCredentialsAvailable, Status: "False", Reason: v1.ReasonCredentialSecretNotFound, Message: "No secret"}}, types.CredentialStateResult{Available: false, Reason: v1.ReasonCredentialSecretNotFound, Message: "No secret"}},
		{"empty", []v1.WorkspaceCondition{{Type: v1.WorkspaceConditionCredentialsAvailable, Status: "False", Reason: v1.ReasonCredentialEmpty}}, types.CredentialStateResult{Available: false, Reason: v1.ReasonCredentialEmpty}},
		{"no condition", nil, types.CredentialStateResult{Available: false, Reason: "NotChecked"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, credStateFromConditions(tt.conditions))
		})
	}
}

func TestAgentHealthFromConditions(t *testing.T) {
	past := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	tests := []struct {
		name       string
		conditions []v1.WorkspaceCondition
		lastCheck  *metav1.Time
		expected   types.AgentHealthResult
	}{
		{"healthy", []v1.WorkspaceCondition{{Type: v1.WorkspaceConditionAgentHealthy, Status: "True", Reason: v1.ReasonAgentHealthy, Message: "connected=[opencode] sessions=2 version=1.2.27"}}, &past, types.AgentHealthResult{Status: "Healthy", Message: "connected=[opencode] sessions=2 version=1.2.27", Connected: []string{"opencode"}, AgentVersion: "1.2.27", LastCheckedAt: past.Format(time.RFC3339)}},
		{"degraded", []v1.WorkspaceCondition{{Type: v1.WorkspaceConditionAgentHealthy, Status: "False", Reason: v1.ReasonAgentDegraded, Message: "no providers connected (configured=1, connected=[])"}}, nil, types.AgentHealthResult{Status: "Degraded", Message: "no providers connected (configured=1, connected=[])", ProvidersConfigured: 1}},
		{"unhealthy", []v1.WorkspaceCondition{{Type: v1.WorkspaceConditionAgentHealthy, Status: "False", Reason: v1.ReasonAgentUnhealthy, Message: "agent dead"}}, nil, types.AgentHealthResult{Status: "Unhealthy", Message: "agent dead"}},
		{"check failed", []v1.WorkspaceCondition{{Type: v1.WorkspaceConditionAgentHealthy, Status: "Unknown", Reason: v1.ReasonHealthCheckFailed, Message: "refused"}}, nil, types.AgentHealthResult{Status: "Unknown", Message: "refused"}},
		{"no condition", nil, nil, types.AgentHealthResult{Status: "Unknown"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, agentHealthFromConditions(tt.conditions, tt.lastCheck))
		})
	}
}
