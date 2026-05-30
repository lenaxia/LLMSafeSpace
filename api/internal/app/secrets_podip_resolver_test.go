package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWorkspaceCRDGetter is a stub workspaceCRDGetter for resolver tests.
type fakeWorkspaceCRDGetter struct {
	ws  *v1.Workspace
	err error
}

func (f *fakeWorkspaceCRDGetter) GetWorkspace(id string) (*v1.Workspace, error) {
	return f.ws, f.err
}

// fakeDBOwnerLookup is a stub dbOwnerLookup for resolver tests.
type fakeDBOwnerLookup struct {
	meta *types.WorkspaceMetadata
	err  error
}

func (f *fakeDBOwnerLookup) GetWorkspace(_ context.Context, _ string) (*types.WorkspaceMetadata, error) {
	return f.meta, f.err
}

// captureLogger records Warn-level entries so tests can assert on
// whether a transient failure was surfaced (Finding 2 in worklog 0094
// follow-up audit).
type captureLogger struct {
	mu    sync.Mutex
	warns []string
}

func (l *captureLogger) Debug(string, ...interface{}) {}
func (l *captureLogger) Info(string, ...interface{})  {}
func (l *captureLogger) Warn(msg string, _ ...interface{}) {
	l.mu.Lock()
	l.warns = append(l.warns, msg)
	l.mu.Unlock()
}
func (l *captureLogger) Error(string, error, ...interface{}) {}
func (l *captureLogger) Fatal(string, error, ...interface{}) {}
func (l *captureLogger) Sync() error                         { return nil }
func (l *captureLogger) With(_ ...interface{}) pkginterfaces.LoggerInterface {
	return l
}

func (l *captureLogger) warnCount() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.warns) }

func activeWorkspace(podIP string) *v1.Workspace {
	return &v1.Workspace{
		Status: v1.WorkspaceStatus{
			Phase: v1.WorkspacePhaseActive,
			PodIP: podIP,
		},
	}
}

// TestSecretsPodIPResolver_OwnerActive_ReturnsPodIP is the happy-path
// regression test for Bug 1 (worklog 0085): the resolver must return the
// pod IP when the caller owns an Active workspace, otherwise the
// reload-secrets endpoint cannot reach agentd.
func TestSecretsPodIPResolver_OwnerActive_ReturnsPodIP(t *testing.T) {
	r := newSecretsPodIPResolver(
		&fakeWorkspaceCRDGetter{ws: activeWorkspace("10.0.1.42")},
		&fakeDBOwnerLookup{meta: &types.WorkspaceMetadata{UserID: "u1"}},
		nil,
	)

	ip, err := r.GetWorkspacePodIP(context.Background(), "u1", "ws-1")

	require.NoError(t, err)
	assert.Equal(t, "10.0.1.42", ip)
}

func TestSecretsPodIPResolver_NotOwner_ReturnsEmpty(t *testing.T) {
	r := newSecretsPodIPResolver(
		&fakeWorkspaceCRDGetter{ws: activeWorkspace("10.0.1.42")},
		&fakeDBOwnerLookup{meta: &types.WorkspaceMetadata{UserID: "other-user"}},
		nil,
	)

	ip, err := r.GetWorkspacePodIP(context.Background(), "u1", "ws-1")

	require.NoError(t, err)
	assert.Empty(t, ip, "non-owner must not get pod IP")
}

func TestSecretsPodIPResolver_WorkspaceMissing_ReturnsEmpty(t *testing.T) {
	r := newSecretsPodIPResolver(
		&fakeWorkspaceCRDGetter{ws: activeWorkspace("10.0.1.42")},
		&fakeDBOwnerLookup{meta: nil},
		nil,
	)

	ip, err := r.GetWorkspacePodIP(context.Background(), "u1", "ws-1")

	require.NoError(t, err)
	assert.Empty(t, ip)
}

func TestSecretsPodIPResolver_NotActive_ReturnsEmpty(t *testing.T) {
	suspended := &v1.Workspace{
		Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseSuspended, PodIP: "10.0.1.42"},
	}
	r := newSecretsPodIPResolver(
		&fakeWorkspaceCRDGetter{ws: suspended},
		&fakeDBOwnerLookup{meta: &types.WorkspaceMetadata{UserID: "u1"}},
		nil,
	)

	ip, err := r.GetWorkspacePodIP(context.Background(), "u1", "ws-1")

	require.NoError(t, err)
	assert.Empty(t, ip, "non-Active workspace must not return pod IP")
}

func TestSecretsPodIPResolver_CRDError_ReturnsEmpty(t *testing.T) {
	r := newSecretsPodIPResolver(
		&fakeWorkspaceCRDGetter{err: errors.New("apiserver unreachable")},
		&fakeDBOwnerLookup{meta: &types.WorkspaceMetadata{UserID: "u1"}},
		nil,
	)

	ip, err := r.GetWorkspacePodIP(context.Background(), "u1", "ws-1")

	require.NoError(t, err, "CRD errors are downgraded to no-running-pod")
	assert.Empty(t, ip)
}

// TestSecretsPodIPResolver_DBError_DowngradesAndLogs verifies the
// security/observability trade-off described in Finding 2 of the
// worklog 0094 follow-up audit: a DB blip must produce an empty
// resolver result (so the response shape is uniform with "you don't
// own this workspace") AND must surface in the logs at Warn (so
// operators can detect a Postgres outage). Pre-fix the resolver
// propagated the error, which caused inconsistent 5xx vs 4xx
// responses depending on whether the workspace existed.
func TestSecretsPodIPResolver_DBError_DowngradesAndLogs(t *testing.T) {
	logger := &captureLogger{}
	r := newSecretsPodIPResolver(
		&fakeWorkspaceCRDGetter{ws: activeWorkspace("10.0.1.42")},
		&fakeDBOwnerLookup{err: errors.New("db down")},
		logger,
	)

	ip, err := r.GetWorkspacePodIP(context.Background(), "u1", "ws-1")

	require.NoError(t, err, "DB errors must be downgraded so the response shape is uniform across not-owned / not-found / DB-blip")
	assert.Empty(t, ip)
	assert.GreaterOrEqual(t, logger.warnCount(), 1, "DB blip must surface as Warn so operators can detect outages")
}

func TestSecretsPodIPResolver_EmptyInputs_ReturnsEmpty(t *testing.T) {
	r := newSecretsPodIPResolver(
		&fakeWorkspaceCRDGetter{ws: activeWorkspace("10.0.1.42")},
		&fakeDBOwnerLookup{meta: &types.WorkspaceMetadata{UserID: "u1"}},
		nil,
	)

	ip, err := r.GetWorkspacePodIP(context.Background(), "", "ws-1")
	require.NoError(t, err)
	assert.Empty(t, ip)

	ip, err = r.GetWorkspacePodIP(context.Background(), "u1", "")
	require.NoError(t, err)
	assert.Empty(t, ip)
}
