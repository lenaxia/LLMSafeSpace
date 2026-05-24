package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// Tests for fix #3: credential secret watch triggers sandbox restart.

// ---------------------------------------------------------------------------
// happy path: credential secret changes → RestartGeneration bumped
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_CredentialSecretChanged_TriggersRestart(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-cred", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.WorkspaceRef = "ws-cred"
	sb.Labels = map[string]string{common.LabelWorkspace: "ws-cred"}
	sb.Status.PodName = "cred-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now
	sb.Status.CredentialSecretHash = "oldhashvalue1234567890abcdef1234567890abcdef1234567890abcdef12345678"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cred-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	// Credential secret with different data than what the hash represents.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws-cred", Namespace: "default"},
		Data:       map[string][]byte{"provider-config": []byte(`{"new":"config"}`)},
	}

	r := reconcilerFor(t, sb, pod, secret)
	result, err := r.Reconcile(context.Background(), reqFor("sb-cred", "default"))
	require.NoError(t, err)
	assert.True(t, result.Requeue)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-cred", Namespace: "default"}, updated))

	assert.NotEqual(t, int64(0), updated.Spec.RestartGeneration,
		"RestartGeneration must be bumped when credential hash changes")
	assert.NotEqual(t, "oldhashvalue1234567890abcdef1234567890abcdef1234567890abcdef12345678",
		updated.Status.CredentialSecretHash,
		"CredentialSecretHash must be updated to new value")
}

// ---------------------------------------------------------------------------
// happy path: credential secret unchanged → no restart
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_CredentialSecretUnchanged_NoRestart(t *testing.T) {
	now := metav1.Now()

	secretData := map[string][]byte{"provider-config": []byte(`{"same":"config"}`)}
	hash := hashSecretData(secretData)

	sb := makeSandbox("sb-cred-same", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.WorkspaceRef = "ws-same"
	sb.Labels = map[string]string{common.LabelWorkspace: "ws-same"}
	sb.Status.PodName = "same-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now
	sb.Status.CredentialSecretHash = hash

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "same-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws-same", Namespace: "default"},
		Data:       secretData,
	}

	r := reconcilerFor(t, sb, pod, secret)
	_, err := r.Reconcile(context.Background(), reqFor("sb-cred-same", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-cred-same", Namespace: "default"}, updated))

	assert.Equal(t, int64(0), updated.Spec.RestartGeneration,
		"unchanged credential must not bump RestartGeneration")
	assert.Equal(t, common.SandboxPhaseRunning, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// happy path: first observation (hash empty) → record hash, no restart
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_CredentialSecret_FirstObservation_NoRestart(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-cred-first", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.WorkspaceRef = "ws-first"
	sb.Labels = map[string]string{common.LabelWorkspace: "ws-first"}
	sb.Status.PodName = "first-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now
	sb.Status.CredentialSecretHash = "" // first time

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "first-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws-first", Namespace: "default"},
		Data:       map[string][]byte{"provider-config": []byte(`{"initial":"config"}`)},
	}

	r := reconcilerFor(t, sb, pod, secret)
	_, err := r.Reconcile(context.Background(), reqFor("sb-cred-first", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-cred-first", Namespace: "default"}, updated))

	assert.Equal(t, int64(0), updated.Spec.RestartGeneration,
		"first observation must not trigger restart")
	assert.NotEmpty(t, updated.Status.CredentialSecretHash,
		"hash must be recorded on first observation")
}

// ---------------------------------------------------------------------------
// edge case: no credential secret exists → no restart, hash cleared
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_NoCredentialSecret_NoRestart(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-no-cred", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.WorkspaceRef = "ws-nocred"
	sb.Labels = map[string]string{common.LabelWorkspace: "ws-nocred"}
	sb.Status.PodName = "nocred-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "nocred-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	// No credential secret in the fake client.
	r := reconcilerFor(t, sb, pod)
	_, err := r.Reconcile(context.Background(), reqFor("sb-no-cred", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-no-cred", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhaseRunning, updated.Status.Phase)
	assert.Equal(t, int64(0), updated.Spec.RestartGeneration)
}

// ---------------------------------------------------------------------------
// mapper test: mapCredSecretToSandboxes returns correct requests
// ---------------------------------------------------------------------------

func TestMapCredSecretToSandboxes_MatchingSandboxes(t *testing.T) {
	sb1 := makeSandbox("sb-ws1-a", "default", common.SandboxPhaseRunning)
	sb1.Labels = map[string]string{common.LabelWorkspace: "ws1"}
	sb2 := makeSandbox("sb-ws1-b", "default", common.SandboxPhaseRunning)
	sb2.Labels = map[string]string{common.LabelWorkspace: "ws1"}
	sb3 := makeSandbox("sb-ws2", "default", common.SandboxPhaseRunning)
	sb3.Labels = map[string]string{common.LabelWorkspace: "ws2"}

	r := reconcilerFor(t, sb1, sb2, sb3)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws1", Namespace: "default"},
	}

	requests := r.mapCredSecretToSandboxes(context.Background(), secret)
	assert.Len(t, requests, 2, "must return sandboxes matching workspace ws1")
}

func TestMapCredSecretToSandboxes_NonCredSecret_ReturnsNil(t *testing.T) {
	r := reconcilerFor(t)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sandbox-pw-something", Namespace: "default"},
	}

	requests := r.mapCredSecretToSandboxes(context.Background(), secret)
	assert.Nil(t, requests, "non-credential secrets must not trigger any reconcile")
}
