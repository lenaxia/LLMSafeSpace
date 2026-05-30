package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// --- Helpers ---

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func reconcilerFor(t *testing.T, objs ...runtime.Object) *WorkspaceReconciler {
	t.Helper()
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	return &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}
}

func reqFor(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}
}

func makeWorkspace(name, namespace string, phase v1.WorkspacePhase) *v1.Workspace {
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			UID:               "aaaabbbb-cccc-dddd-eeee-ffffgggghhhh",
			CreationTimestamp: metav1.Now(),
		},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "user-1"},
			Runtime: "python:3.11",
			Storage: v1.WorkspaceStorageConfig{Size: "5Gi", AccessMode: "ReadWriteOnce"},
		},
		Status: v1.WorkspaceStatus{Phase: phase},
	}
}

func makeBoundPVC(name, namespace string, ownerUID types.UID) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")}},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
	if ownerUID != "" {
		pvc.OwnerReferences = []metav1.OwnerReference{{UID: ownerUID}}
	}
	return pvc
}

func makeRunningPod(name, namespace, ip string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: ip},
	}
}

func makePasswordSecret(wsName, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: passwordSecretName(wsName), Namespace: namespace},
		Data:       map[string][]byte{"password": []byte("test-password")},
	}
}

// --- Pending Phase Tests ---

func TestReconcile_NotFound_NoError(t *testing.T) {
	r := reconcilerFor(t)
	_, err := r.Reconcile(context.Background(), reqFor("gone", "default"))
	assert.NoError(t, err)
}

func TestReconcile_Pending_CreatesPVC(t *testing.T) {
	ws := makeWorkspace("ws-new", "default", "")
	r := reconcilerFor(t, ws)

	_, err := r.Reconcile(context.Background(), reqFor("ws-new", "default"))
	require.NoError(t, err)

	pvc := &corev1.PersistentVolumeClaim{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "workspace-ws-new", Namespace: "default"}, pvc)
	assert.NoError(t, err, "PVC should be created")
}

func TestReconcile_Pending_PVCBound_TransitionsToCreating(t *testing.T) {
	ws := makeWorkspace("ws-bound", "default", v1.WorkspacePhasePending)
	pvc := makeBoundPVC("workspace-ws-bound", "default", ws.UID)
	r := reconcilerFor(t, ws, pvc)

	_, err := r.Reconcile(context.Background(), reqFor("ws-bound", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-bound", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseCreating, updated.Status.Phase)
}

func TestReconcile_Pending_Timeout_TransitionsToFailed(t *testing.T) {
	ws := makeWorkspace("ws-timeout", "default", v1.WorkspacePhasePending)
	ws.CreationTimestamp = metav1.NewTime(time.Now().Add(-10 * time.Minute))
	r := reconcilerFor(t, ws)

	_, err := r.Reconcile(context.Background(), reqFor("ws-timeout", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-timeout", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseFailed, updated.Status.Phase)
}

// --- Creating Phase Tests ---

func TestReconcile_Creating_NoPod_CreatesPod(t *testing.T) {
	ws := makeWorkspace("ws-creating", "default", v1.WorkspacePhaseCreating)
	ws.Status.PVCName = "workspace-ws-creating"
	pvc := makeBoundPVC("workspace-ws-creating", "default", ws.UID)
	pwSecret := makePasswordSecret("ws-creating", "default")
	// Need a RuntimeEnvironment for image resolution.
	rte := &v1.RuntimeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "python-3.11"},
		Spec:       v1.RuntimeEnvironmentSpec{Image: "ghcr.io/test/python:3.11", Language: "python", Version: "3.11"},
	}
	r := reconcilerFor(t, ws, pvc, pwSecret, rte)

	_, err := r.Reconcile(context.Background(), reqFor("ws-creating", "default"))
	require.NoError(t, err)

	// Pod should be created.
	expectedPodName := podName("ws-creating", string(ws.UID))
	pod := &corev1.Pod{}
	err = r.Get(context.Background(), types.NamespacedName{Name: expectedPodName, Namespace: "default"}, pod)
	assert.NoError(t, err, "Pod should be created")
	assert.Equal(t, "ghcr.io/test/python:3.11", pod.Spec.Containers[0].Image)
}

func TestReconcile_Creating_PodRunning_TransitionsToActive(t *testing.T) {
	ws := makeWorkspace("ws-running", "default", v1.WorkspacePhaseCreating)
	ws.Status.PVCName = "workspace-ws-running"
	expectedPodName := podName("ws-running", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.5")
	r := reconcilerFor(t, ws, pod)

	_, err := r.Reconcile(context.Background(), reqFor("ws-running", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-running", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseActive, updated.Status.Phase)
	assert.Equal(t, "10.0.0.5", updated.Status.PodIP)
	assert.Equal(t, "http://10.0.0.5:4096", updated.Status.Endpoint)
}

// --- Active Phase Tests ---

func TestReconcile_Active_PodRunning_RequeuesAfter30s(t *testing.T) {
	ws := makeWorkspace("ws-active", "default", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	expectedPodName := podName("ws-active", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	r := reconcilerFor(t, ws, pod)

	result, err := r.Reconcile(context.Background(), reqFor("ws-active", "default"))
	require.NoError(t, err)
	assert.Equal(t, requeueActive, result.RequeueAfter)
}

func TestReconcile_Active_PodMissing_TransientRecovery(t *testing.T) {
	ws := makeWorkspace("ws-lost", "default", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	ws.Spec.MaxRetries = 3
	r := reconcilerFor(t, ws) // no pod

	_, err := r.Reconcile(context.Background(), reqFor("ws-lost", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-lost", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseCreating, updated.Status.Phase, "should self-heal to Creating")
	assert.Equal(t, int32(1), updated.Status.TransientFailureCount)
}

func TestReconcile_Active_PodMissing_MaxRetries_Failed(t *testing.T) {
	ws := makeWorkspace("ws-exhausted", "default", v1.WorkspacePhaseActive)
	ws.Status.TransientFailureCount = 2
	ws.Spec.MaxRetries = 3
	r := reconcilerFor(t, ws) // no pod

	_, err := r.Reconcile(context.Background(), reqFor("ws-exhausted", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-exhausted", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseFailed, updated.Status.Phase)
}

func TestReconcile_Active_Timeout_Suspends(t *testing.T) {
	ws := makeWorkspace("ws-timeout", "default", v1.WorkspacePhaseActive)
	ws.Spec.Timeout = 60
	past := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	ws.Status.StartTime = &past
	expectedPodName := podName("ws-timeout", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	r := reconcilerFor(t, ws, pod)

	_, err := r.Reconcile(context.Background(), reqFor("ws-timeout", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-timeout", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseSuspending, updated.Status.Phase)
}

func TestReconcile_Active_RestartGeneration_RecreatesPod(t *testing.T) {
	ws := makeWorkspace("ws-restart", "default", v1.WorkspacePhaseActive)
	ws.Spec.RestartGeneration = 2
	ws.Status.ObservedRestartGeneration = 1
	ws.Status.PodIP = "10.0.0.1"
	expectedPodName := podName("ws-restart", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	r := reconcilerFor(t, ws, pod)

	_, err := r.Reconcile(context.Background(), reqFor("ws-restart", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-restart", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseCreating, updated.Status.Phase)
	assert.Equal(t, int64(2), updated.Status.ObservedRestartGeneration)
	assert.Empty(t, updated.Status.PodIP)
}

// --- Suspend/Resume/Terminate Tests ---

func TestReconcile_Suspending_DeletesPodAndTransitions(t *testing.T) {
	ws := makeWorkspace("ws-susp", "default", v1.WorkspacePhaseSuspending)
	expectedPodName := podName("ws-susp", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	r := reconcilerFor(t, ws, pod)

	_, err := r.Reconcile(context.Background(), reqFor("ws-susp", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-susp", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseSuspended, updated.Status.Phase)
	assert.Empty(t, updated.Status.PodIP)
	assert.NotNil(t, updated.Status.SuspendedAt)
}

func TestReconcile_Suspended_TTLExpired_Terminates(t *testing.T) {
	ws := makeWorkspace("ws-ttl", "default", v1.WorkspacePhaseSuspended)
	ws.Spec.TTLSecondsAfterSuspended = 60
	past := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	ws.Status.SuspendedAt = &past
	r := reconcilerFor(t, ws)

	_, err := r.Reconcile(context.Background(), reqFor("ws-ttl", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-ttl", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseTerminating, updated.Status.Phase)
}

func TestReconcile_Suspended_NoTTL_NoAction(t *testing.T) {
	ws := makeWorkspace("ws-notttl", "default", v1.WorkspacePhaseSuspended)
	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-notttl", "default"))
	require.NoError(t, err)
	assert.Zero(t, result.RequeueAfter)
}

func TestReconcile_Resuming_TransitionsToCreating(t *testing.T) {
	ws := makeWorkspace("ws-resume", "default", v1.WorkspacePhaseResuming)
	past := metav1.Now()
	ws.Status.SuspendedAt = &past
	// Activity timestamp from before suspension. handleResuming must reset
	// this; otherwise handleActive will see a long idle and re-suspend.
	staleActivity := metav1.NewTime(time.Now().Add(-3 * time.Hour))
	ws.Status.LastActivityAt = &staleActivity
	pwSecret := makePasswordSecret("ws-resume", "default")
	r := reconcilerFor(t, ws, pwSecret)

	_, err := r.Reconcile(context.Background(), reqFor("ws-resume", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-resume", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseCreating, updated.Status.Phase)
	assert.Nil(t, updated.Status.SuspendedAt)
	require.NotNil(t, updated.Status.LastActivityAt, "LastActivityAt must be reset on resume")
	assert.WithinDuration(t, time.Now(), updated.Status.LastActivityAt.Time, 5*time.Second,
		"LastActivityAt must advance to current time on resume")
}

func TestReconcile_Terminating_CleansUp(t *testing.T) {
	ws := makeWorkspace("ws-term", "default", v1.WorkspacePhaseTerminating)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-term"
	pvc := makeBoundPVC("workspace-ws-term", "default", ws.UID)
	pwSecret := makePasswordSecret("ws-term", "default")
	r := reconcilerFor(t, ws, pvc, pwSecret)

	_, err := r.Reconcile(context.Background(), reqFor("ws-term", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-term", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseTerminated, updated.Status.Phase)
	assert.NotContains(t, updated.Finalizers, WorkspaceFinalizer)
}

func TestReconcile_Failed_NoAction(t *testing.T) {
	ws := makeWorkspace("ws-fail", "default", v1.WorkspacePhaseFailed)
	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-fail", "default"))
	assert.NoError(t, err)
	assert.Zero(t, result.RequeueAfter)
}

// TestReconcile_Failed_CleansUpSecrets is the regression test for Bug 12
// in worklog 0085: workspaces stuck in Failed phase used to leak the
// per-workspace K8s Secrets indefinitely (45h+ in the wild). Failed phase
// must purge them so future reconciles converge on a clean state.
func TestReconcile_Failed_CleansUpSecrets(t *testing.T) {
	ws := makeWorkspace("ws-fail-cleanup", "default", v1.WorkspacePhaseFailed)
	pwSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-pw-ws-fail-cleanup", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("p")},
	}
	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws-fail-cleanup", Namespace: "default"},
		Data:       map[string][]byte{"creds": []byte("c")},
	}
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-secrets-ws-fail-cleanup", Namespace: "default"},
		Data:       map[string][]byte{"secrets.json": []byte("[]")},
	}

	r := reconcilerFor(t, ws, pwSecret, credSecret, userSecret)

	_, err := r.Reconcile(context.Background(), reqFor("ws-fail-cleanup", "default"))
	require.NoError(t, err)

	// All three Secrets must be gone after one reconcile.
	for _, name := range []string{
		"workspace-pw-ws-fail-cleanup",
		"workspace-creds-ws-fail-cleanup",
		"workspace-secrets-ws-fail-cleanup",
	} {
		var got corev1.Secret
		err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, &got)
		assert.True(t, err != nil, "Bug 12: %s must be deleted on Failed reconcile", name)
	}
}
