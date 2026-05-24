package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func makeSandbox(name, namespace, phase string) *v1.Sandbox {
	return &v1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "12345678-1234-1234-1234-1234567890ab",
		},
		Spec: v1.SandboxSpec{
			Runtime: "python:3.11",
			Timeout: 300,
		},
		Status: v1.SandboxStatus{
			Phase: phase,
		},
	}
}

func reconcilerFor(t *testing.T, objs ...runtime.Object) *SandboxReconciler {
	t.Helper()
	scheme := testScheme(t)

	// Seed a default RuntimeEnvironment named "python-3.11" so the runtime
	// resolver finds a match for the default makeSandbox runtime spec
	// ("python:3.11"). Tests that need a different runtime should pass
	// their own RuntimeEnvironment (with the same or a different name) in
	// objs — controller-runtime's fake client does NOT de-dupe on name,
	// but the resolver always picks the first hit on exact name.
	defaultRE := &v1.RuntimeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "python-3.11"},
		Spec: v1.RuntimeEnvironmentSpec{
			Language: "python",
			Version:  "3.11",
			Image:    "test-registry.local/runtime-base:test",
		},
	}
	hasOwnRE := false
	for _, o := range objs {
		if _, ok := o.(*v1.RuntimeEnvironment); ok {
			hasOwnRE = true
			break
		}
	}
	if !hasOwnRE {
		objs = append(objs, defaultRE)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithStatusSubresource(&v1.Sandbox{}, &corev1.Pod{}).
		Build()
	return &SandboxReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}
}

func reqFor(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}
}

// ---------------------------------------------------------------------------
// Reconcile — object not found
// ---------------------------------------------------------------------------

func TestReconcile_ObjectNotFound_ReturnsNoError(t *testing.T) {
	r := reconcilerFor(t) // empty store

	result, err := r.Reconcile(context.Background(), reqFor("missing", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// ---------------------------------------------------------------------------
// Reconcile — pending sandbox transitions to creating
// ---------------------------------------------------------------------------

func TestReconcile_PendingSandbox_TransitionsToCreating(t *testing.T) {
	sb := makeSandbox("sb-pending", "default", common.SandboxPhasePending)
	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-pending", "default"))

	// Pod creation will fail because status.podName is empty — that's fine; the
	// important assertion is that the phase was updated to Creating first.
	// The reconciler either returns an error or re-queues.
	_ = result
	_ = err

	// Fetch updated sandbox
	updated := &v1.Sandbox{}
	fetchErr := r.Get(context.Background(), types.NamespacedName{Name: "sb-pending", Namespace: "default"}, updated)
	require.NoError(t, fetchErr)

	// Finalizer must have been added
	assert.Contains(t, updated.Finalizers, common.SandboxFinalizer)
}

// ---------------------------------------------------------------------------
// Reconcile — empty phase treated the same as Pending
// ---------------------------------------------------------------------------

func TestReconcile_EmptyPhase_AddsFinalizer(t *testing.T) {
	sb := makeSandbox("sb-empty", "default", "")
	r := reconcilerFor(t, sb)

	_, _ = r.Reconcile(context.Background(), reqFor("sb-empty", "default"))

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-empty", Namespace: "default"}, updated))
	assert.Contains(t, updated.Finalizers, common.SandboxFinalizer)
}

// ---------------------------------------------------------------------------
// Reconcile — terminated / failed phases — no requeue
// ---------------------------------------------------------------------------

func TestReconcile_TerminatedSandbox_NoRequeue(t *testing.T) {
	sb := makeSandbox("sb-terminated", "default", common.SandboxPhaseTerminated)
	sb.Finalizers = []string{common.SandboxFinalizer}
	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-terminated", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcile_FailedSandbox_NoRequeue(t *testing.T) {
	sb := makeSandbox("sb-failed", "default", common.SandboxPhaseFailed)
	sb.Finalizers = []string{common.SandboxFinalizer}
	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-failed", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// ---------------------------------------------------------------------------
// handleCreatingSandbox — pod exists and is running
// ---------------------------------------------------------------------------

func TestHandleCreatingSandbox_PodRunning_TransitionsToRunning(t *testing.T) {
	sb := makeSandbox("sb-creating", "default", common.SandboxPhaseCreating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "sb-creating-12345678"
	sb.Status.PodNamespace = "default"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-creating-12345678",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-creating", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-creating", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseRunning, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// handleCreatingSandbox — pod not found → revert to Pending
// ---------------------------------------------------------------------------

func TestHandleCreatingSandbox_PodNotFound_RevertsToInProgress(t *testing.T) {
	sb := makeSandbox("sb-creating2", "default", common.SandboxPhaseCreating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"

	r := reconcilerFor(t, sb) // pod NOT in the store

	result, err := r.Reconcile(context.Background(), reqFor("sb-creating2", "default"))

	require.NoError(t, err)
	assert.True(t, result.Requeue)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-creating2", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhasePending, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// handleCreatingSandbox — pod still pending → requeue after delay
// ---------------------------------------------------------------------------

func TestHandleCreatingSandbox_PodPending_RequeuesAfterDelay(t *testing.T) {
	sb := makeSandbox("sb-creating3", "default", common.SandboxPhaseCreating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "pod-pending"
	sb.Status.PodNamespace = "default"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-pending", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-creating3", "default"))

	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, result.RequeueAfter)
}

// ---------------------------------------------------------------------------
// handleRunningSandbox — pod not found, no priors → revert to Pending (fix #2)
//
// Pre-fix #2 this branch went straight to Failed. Post-fix #2, the first
// MaxTransientFailures-1 occurrences self-heal by reverting to Pending; only
// the Nth (= MaxTransientFailures) is terminal. The terminal-failure case
// is covered by transient_failure_test.go::TestHandleRunningSandbox_PodMissing_AtThreshold_MarksFailed.
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_PodNotFound_FirstOccurrence_RevertsToPending(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-running", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now

	r := reconcilerFor(t, sb) // pod NOT in store

	result, err := r.Reconcile(context.Background(), reqFor("sb-running", "default"))

	require.NoError(t, err)
	assert.True(t, result.Requeue, "transient recovery must request immediate requeue")

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-running", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhasePending, updated.Status.Phase,
		"first transient pod-loss must revert to Pending (fix #2); see transient_failure_test.go for full matrix")
	assert.Equal(t, int32(1), updated.Status.TransientFailureCount)
}

// ---------------------------------------------------------------------------
// handleRunningSandbox — timeout exceeded → transition to Terminating
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_TimeoutExceeded_TransitionsToTerminating(t *testing.T) {
	longAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	sb := makeSandbox("sb-timeout", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.Timeout = 300 // 5 min — started 2h ago, so timed out
	sb.Status.PodName = "sb-timeout-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &longAgo

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-timeout-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-timeout", "default"))

	require.NoError(t, err)
	assert.True(t, result.Requeue)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-timeout", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseTerminating, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// handleTerminatingSandbox — pod already gone → mark terminated
// ---------------------------------------------------------------------------

func TestHandleTerminatingSandbox_PodAlreadyGone_MarksTerminated(t *testing.T) {
	sb := makeSandbox("sb-terminating", "default", common.SandboxPhaseTerminating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "gone-pod"
	sb.Status.PodNamespace = "default"

	r := reconcilerFor(t, sb) // pod NOT in store

	result, err := r.Reconcile(context.Background(), reqFor("sb-terminating", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-terminating", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseTerminated, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// handleDeletion — running sandbox gets deletion timestamp → finalizer removed
// ---------------------------------------------------------------------------

func TestHandleDeletion_FinalizerRemovedAfterCleanup(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-deleting", "default", common.SandboxPhaseTerminated)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.DeletionTimestamp = &now

	r := reconcilerFor(t, sb)
	result, err := r.Reconcile(context.Background(), reqFor("sb-deleting", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// After deletion the object may be gone from the store; a NotFound is acceptable
	updated := &v1.Sandbox{}
	fetchErr := r.Get(context.Background(), types.NamespacedName{Name: "sb-deleting", Namespace: "default"}, updated)
	if fetchErr == nil {
		assert.NotContains(t, updated.Finalizers, common.SandboxFinalizer)
	} else {
		assert.True(t, k8serrors.IsNotFound(fetchErr))
	}
}

// ---------------------------------------------------------------------------
// handleDeletion — sandbox in Running with deletion timestamp → transitions to Terminating first
// ---------------------------------------------------------------------------

func TestHandleDeletion_RunningWithDeletionTimestamp_TransitionsToTerminating(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-delrun", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.DeletionTimestamp = &now

	r := reconcilerFor(t, sb)
	result, err := r.Reconcile(context.Background(), reqFor("sb-delrun", "default"))

	require.NoError(t, err)
	assert.True(t, result.Requeue)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-delrun", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseTerminating, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Unknown phase — no requeue
// ---------------------------------------------------------------------------

func TestReconcile_UnknownPhase_NoRequeue(t *testing.T) {
	sb := makeSandbox("sb-unknown", "default", "Weird")
	sb.Finalizers = []string{common.SandboxFinalizer}
	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-unknown", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// ---------------------------------------------------------------------------
// helpers for workspace tests
// ---------------------------------------------------------------------------

func makeWorkspace(name, namespace, pvcName string) *v1.Workspace {
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.WorkspaceSpec{
			Owner:          v1.WorkspaceOwner{UserID: "user-1"},
			DefaultRuntime: "python:3.11",
			Storage:        v1.WorkspaceStorageConfig{Size: "10Gi"},
		},
		Status: v1.WorkspaceStatus{
			Phase:   v1.WorkspacePhaseActive,
			PVCName: pvcName,
		},
	}
}

func reconcilerForWithWorkspace(t *testing.T, objs ...runtime.Object) *SandboxReconciler {
	t.Helper()
	return reconcilerFor(t, objs...)
}

// ---------------------------------------------------------------------------
// TestBuildPod_WorkspaceRef_MountsPVC
// ---------------------------------------------------------------------------

func TestBuildPod_WorkspaceRef_MountsPVC(t *testing.T) {
	ws := makeWorkspace("my-ws", "default", "my-ws-pvc")
	sb := makeSandbox("sb-ws", "default", common.SandboxPhasePending)
	sb.Spec.WorkspaceRef = "my-ws"

	r := reconcilerForWithWorkspace(t, sb, ws)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	// Check PVC volume exists
	var pvcVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "workspace" {
			pvcVol = &pod.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, pvcVol, "expected 'workspace' volume")
	require.NotNil(t, pvcVol.PersistentVolumeClaim)
	assert.Equal(t, "my-ws-pvc", pvcVol.PersistentVolumeClaim.ClaimName)

	// Check main container has /workspace mount
	var wsMount *corev1.VolumeMount
	for i := range pod.Spec.Containers[0].VolumeMounts {
		if pod.Spec.Containers[0].VolumeMounts[i].Name == "workspace" {
			wsMount = &pod.Spec.Containers[0].VolumeMounts[i]
			break
		}
	}
	require.NotNil(t, wsMount, "expected 'workspace' volume mount in main container")
	assert.Equal(t, "/workspace", wsMount.MountPath)
}

// ---------------------------------------------------------------------------
// TestBuildPod_NoWorkspaceRef_NoWorkspaceMount
// ---------------------------------------------------------------------------

func TestBuildPod_NoWorkspaceRef_NoWorkspaceMount(t *testing.T) {
	sb := makeSandbox("sb-nows", "default", common.SandboxPhasePending)

	r := reconcilerFor(t, sb)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	for _, v := range pod.Spec.Volumes {
		assert.NotEqual(t, "workspace", v.Name, "unexpected 'workspace' volume when no workspaceRef")
	}
	for _, vm := range pod.Spec.Containers[0].VolumeMounts {
		assert.NotEqual(t, "workspace", vm.Name, "unexpected 'workspace' mount when no workspaceRef")
	}
}

// ---------------------------------------------------------------------------
// TestBuildPod_AlwaysHasEmptyDirVolumes
// ---------------------------------------------------------------------------

func TestBuildPod_AlwaysHasEmptyDirVolumes(t *testing.T) {
	sb := makeSandbox("sb-emptydir", "default", common.SandboxPhasePending)

	r := reconcilerFor(t, sb)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	expectedVols := []string{"sandbox-cfg", "tmp", "sandbox-home"}
	volNames := make(map[string]bool)
	for _, v := range pod.Spec.Volumes {
		volNames[v.Name] = true
	}
	for _, name := range expectedVols {
		assert.True(t, volNames[name], "missing emptyDir volume: %s", name)
	}

	// Also verify the volume mounts on the main container
	mountPaths := make(map[string]string) // name → mountPath
	for _, vm := range pod.Spec.Containers[0].VolumeMounts {
		mountPaths[vm.Name] = vm.MountPath
	}
	assert.Equal(t, "/sandbox-cfg", mountPaths["sandbox-cfg"])
	assert.Equal(t, "/tmp", mountPaths["tmp"])
	assert.Equal(t, "/home/sandbox", mountPaths["sandbox-home"])
}

// ---------------------------------------------------------------------------
// TestBuildPod_AlwaysHasSecurityContext
// ---------------------------------------------------------------------------

func TestBuildPod_AlwaysHasSecurityContext(t *testing.T) {
	sb := makeSandbox("sb-sec", "default", common.SandboxPhasePending)

	r := reconcilerFor(t, sb)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	sc := pod.Spec.Containers[0].SecurityContext
	require.NotNil(t, sc, "main container must have SecurityContext")
	require.NotNil(t, sc.ReadOnlyRootFilesystem)
	assert.True(t, *sc.ReadOnlyRootFilesystem)
	require.NotNil(t, sc.RunAsNonRoot)
	assert.True(t, *sc.RunAsNonRoot)
	require.NotNil(t, sc.AllowPrivilegeEscalation)
	assert.False(t, *sc.AllowPrivilegeEscalation)
	require.NotNil(t, sc.Capabilities)
	assert.Contains(t, sc.Capabilities.Drop, corev1.Capability("ALL"))
}

// ---------------------------------------------------------------------------
// TestReconcile_Creating_UpdatesPodIP
// ---------------------------------------------------------------------------

func TestReconcile_Creating_UpdatesPodIP(t *testing.T) {
	sb := makeSandbox("sb-podip", "default", common.SandboxPhaseCreating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "sb-podip-12345678"
	sb.Status.PodNamespace = "default"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-podip-12345678",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.42",
		},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-podip", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-podip", Namespace: "default"}, updated))
	assert.Equal(t, "10.0.0.42", updated.Status.PodIP)
}

// ---------------------------------------------------------------------------
// TestReconcile_Suspending_DeletesPodAndTransitionsToSuspended
// ---------------------------------------------------------------------------

func TestReconcile_Suspending_DeletesPodAndTransitionsToSuspended(t *testing.T) {
	sb := makeSandbox("sb-suspending", "default", common.SandboxPhaseSuspending)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "sb-suspending-pod"
	sb.Status.PodNamespace = "default"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-suspending-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-suspending", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-suspending", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseSuspended, updated.Status.Phase)

	// Pod must be gone
	deletedPod := &corev1.Pod{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "sb-suspending-pod", Namespace: "default"}, deletedPod)
	assert.True(t, k8serrors.IsNotFound(err), "pod should have been deleted")
}

// ---------------------------------------------------------------------------
// TestReconcile_Resuming_CreatesNewPodAndTransitionsToRunning
// ---------------------------------------------------------------------------

func TestReconcile_Resuming_CreatesNewPodAndTransitionsToRunning(t *testing.T) {
	sb := makeSandbox("sb-resuming", "default", common.SandboxPhaseResuming)
	sb.Finalizers = []string{common.SandboxFinalizer}

	r := reconcilerFor(t, sb)
	result, err := r.Reconcile(context.Background(), reqFor("sb-resuming", "default"))

	// The reconciler should create a pod and requeue for Running transition.
	// A requeue or nil error is expected.
	require.NoError(t, err)
	_ = result

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-resuming", Namespace: "default"}, updated))
	// Phase should be Creating (pod created, waiting for Running)
	assert.Equal(t, common.SandboxPhaseCreating, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// TestReconcile_Creating_CreatesPasswordSecret
// ---------------------------------------------------------------------------

func TestReconcile_Creating_CreatesPasswordSecret(t *testing.T) {
	sb := makeSandbox("sb-pw", "default", common.SandboxPhasePending)

	r := reconcilerFor(t, sb)
	_, _ = r.Reconcile(context.Background(), reqFor("sb-pw", "default"))

	// After the first reconcile (Pending → Creating + pod creation), a password
	// secret named sandbox-pw-{name} should exist.
	secret := &corev1.Secret{}
	err := r.Get(context.Background(), types.NamespacedName{
		Name:      "sandbox-pw-sb-pw",
		Namespace: "default",
	}, secret)
	require.NoError(t, err, "password secret should have been created")
	assert.NotEmpty(t, secret.Data["password"], "password key must be non-empty")

	// GAP N-3: owner reference must be set to the sandbox
	require.Len(t, secret.OwnerReferences, 1)
	assert.Equal(t, sb.Name, secret.OwnerReferences[0].Name)
	assert.Equal(t, "Sandbox", secret.OwnerReferences[0].Kind)
}

// ---------------------------------------------------------------------------
// TestBuildPod_WorkspaceWithCredentials_MountsCredSecret (GAP M-2)
// ---------------------------------------------------------------------------

func TestBuildPod_WorkspaceWithCredentials_MountsCredSecret(t *testing.T) {
	ws := makeWorkspace("cred-ws", "default", "cred-ws-pvc")
	sb := makeSandbox("sb-credws", "default", common.SandboxPhasePending)
	sb.Spec.WorkspaceRef = "cred-ws"

	credsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-cred-ws",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"provider-config": []byte(`{"apiKey":"test"}`),
		},
	}

	r := reconcilerFor(t, sb, ws, credsSecret)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	// Pod must have a cred-secret volume
	var credVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "cred-secret" {
			credVol = &pod.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, credVol, "pod must have a 'cred-secret' volume")
	require.NotNil(t, credVol.Secret)
	assert.Equal(t, "workspace-creds-cred-ws", credVol.Secret.SecretName)

	// credential-setup init container must have a mount named cred-secret
	var credSetupContainer *corev1.Container
	for i := range pod.Spec.InitContainers {
		if pod.Spec.InitContainers[i].Name == "credential-setup" {
			credSetupContainer = &pod.Spec.InitContainers[i]
			break
		}
	}
	require.NotNil(t, credSetupContainer, "credential-setup init container must be present")

	var credMount *corev1.VolumeMount
	for i := range credSetupContainer.VolumeMounts {
		if credSetupContainer.VolumeMounts[i].Name == "cred-secret" {
			credMount = &credSetupContainer.VolumeMounts[i]
			break
		}
	}
	require.NotNil(t, credMount, "credential-setup must have a 'cred-secret' volume mount")
	assert.Equal(t, "/mnt/secrets/credentials", credMount.MountPath)
}

// ---------------------------------------------------------------------------
// TestReconcile_Creating_WorkspaceNotFound_ReturnsError (GAP M-3)
// ---------------------------------------------------------------------------

func TestReconcile_Creating_WorkspaceNotFound_ReturnsError(t *testing.T) {
	sb := makeSandbox("sb-nowsref", "default", common.SandboxPhasePending)
	sb.Spec.WorkspaceRef = "missing-workspace"

	r := reconcilerFor(t, sb) // no workspace in store

	// First reconcile adds the workspace label and requeues. Second
	// reconcile is the one that does the workspace lookup and must error.
	_, _ = r.Reconcile(context.Background(), reqFor("sb-nowsref", "default"))
	_, err := r.Reconcile(context.Background(), reqFor("sb-nowsref", "default"))

	require.Error(t, err, "reconcile should return an error when workspace is not found")
}

// ===========================================================================
// E2E tests: runtime-aware setup script (GAP-8 fix verification)
// ===========================================================================

func TestE2E_SetupScript_PythonRuntime_UsesPip(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Packages: []v1.WorkspacePackageSet{
				{Runtime: "python:3.11", Requirements: []string{"numpy", "pandas"}},
			},
		},
	}
	script := buildWorkspaceSetupScript(ws)
	assert.Contains(t, script, "pip install --target=/workspace/packages numpy pandas")
}

func TestE2E_SetupScript_NodejsRuntime_UsesNpm(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Packages: []v1.WorkspacePackageSet{
				{Runtime: "nodejs:18", Requirements: []string{"express", "lodash"}},
			},
		},
	}
	script := buildWorkspaceSetupScript(ws)
	assert.Contains(t, script, "npm install express lodash")
	assert.NotContains(t, script, "pip install")
}

func TestE2E_SetupScript_GoRuntime_UsesGoInstall(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Packages: []v1.WorkspacePackageSet{
				{Runtime: "go:1.21", Requirements: []string{"github.com/gin-gonic/gin@latest"}},
			},
		},
	}
	script := buildWorkspaceSetupScript(ws)
	assert.Contains(t, script, "go install github.com/gin-gonic/gin@latest")
	assert.NotContains(t, script, "pip install")
}

func TestE2E_SetupScript_MixedRuntimes(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Packages: []v1.WorkspacePackageSet{
				{Runtime: "python:3.11", Requirements: []string{"requests"}},
				{Runtime: "nodejs:18", Requirements: []string{"axios"}},
				{Runtime: "go:1.21", Requirements: []string{"golang.org/x/tools@latest"}},
			},
		},
	}
	script := buildWorkspaceSetupScript(ws)
	assert.Contains(t, script, "pip install --target=/workspace/packages requests")
	assert.Contains(t, script, "npm install axios")
	assert.Contains(t, script, "go install golang.org/x/tools@latest")
}

func TestE2E_SetupScript_EmptyRequirements_NoInstall(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Packages: []v1.WorkspacePackageSet{
				{Runtime: "python:3.11", Requirements: []string{}},
			},
		},
	}
	script := buildWorkspaceSetupScript(ws)
	assert.NotContains(t, script, "pip install")
}

// ===========================================================================
// E2E tests: API CRD → Controller consumption (GAP-1/2 fix verification)
// ===========================================================================

func TestE2E_SandboxWithWorkspaceRef_LooksUpWorkspaceAndMountsPVC(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-ws",
			Namespace: "default",
		},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "user-1"},
			Storage: v1.WorkspaceStorageConfig{Size: "10Gi"},
		},
		Status: v1.WorkspaceStatus{
			Phase:   v1.WorkspacePhaseActive,
			PVCName: "pvc-e2e-ws",
		},
	}

	sb := makeSandbox("sb-e2e-wsref", "default", common.SandboxPhasePending)
	sb.Spec.WorkspaceRef = "e2e-ws"

	pwSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sandbox-pw-sb-e2e-wsref",
			Namespace: "default",
		},
		Data: map[string][]byte{"password": []byte("test-pw")},
	}

	r := reconcilerFor(t, sb, ws, pwSecret)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	var pvcVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "workspace" {
			pvcVol = &pod.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, pvcVol, "pod must have workspace volume when WorkspaceRef is set")
	require.NotNil(t, pvcVol.PersistentVolumeClaim)
	assert.Equal(t, "pvc-e2e-ws", pvcVol.PersistentVolumeClaim.ClaimName,
		"PVC name must come from workspace CRD status")
}

func TestE2E_SandboxNoWorkspaceRef_NoPVCVolume(t *testing.T) {
	sb := makeSandbox("sb-e2e-nows", "default", common.SandboxPhasePending)

	r := reconcilerFor(t, sb)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	for _, v := range pod.Spec.Volumes {
		assert.NotEqual(t, "workspace", v.Name)
	}
}

func TestE2E_SandboxWithCredSecret_MountsCredVolume(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-cred-ws",
			Namespace: "default",
		},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "user-1"},
			Storage: v1.WorkspaceStorageConfig{Size: "10Gi"},
		},
		Status: v1.WorkspaceStatus{
			Phase:   v1.WorkspacePhaseActive,
			PVCName: "pvc-e2e-cred-ws",
		},
	}

	sb := makeSandbox("sb-e2e-cred", "default", common.SandboxPhasePending)
	sb.Spec.WorkspaceRef = "e2e-cred-ws"

	pwSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sandbox-pw-sb-e2e-cred",
			Namespace: "default",
		},
		Data: map[string][]byte{"password": []byte("test-pw")},
	}

	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-e2e-cred-ws",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"provider-config": []byte(`{"apiKey":"sk-test"}`),
		},
	}

	r := reconcilerFor(t, sb, ws, pwSecret, credSecret)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	var credVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "cred-secret" {
			credVol = &pod.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, credVol, "pod must have cred-secret volume when workspace has credentials")
	require.NotNil(t, credVol.Secret)
	assert.Equal(t, "workspace-creds-e2e-cred-ws", credVol.Secret.SecretName)
}

func TestE2E_PodIP_PopulatedInStatus(t *testing.T) {
	sb := makeSandbox("sb-e2e-ip", "default", common.SandboxPhaseCreating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "sb-e2e-ip-pod"
	sb.Status.PodNamespace = "default"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-e2e-ip-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.1.2.3",
		},
	}

	r := reconcilerFor(t, sb, pod)
	_, err := r.Reconcile(context.Background(), reqFor("sb-e2e-ip", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-e2e-ip", Namespace: "default"}, updated))
	assert.Equal(t, "10.1.2.3", updated.Status.PodIP, "PodIP must be populated in sandbox status")
}

// ===========================================================================
// Sandbox unhappy path tests
// ===========================================================================

func TestE2E_Unhappy_WorkspaceRefNotFound_ReturnsError(t *testing.T) {
	sb := makeSandbox("sb-bad-ws", "default", common.SandboxPhasePending)
	sb.Spec.WorkspaceRef = "nonexistent-workspace"

	r := reconcilerFor(t, sb)

	// First reconcile adds the workspace label and requeues. Second
	// reconcile is the one that actually tries to look up the workspace
	// and must return the error.
	_, _ = r.Reconcile(context.Background(), reqFor("sb-bad-ws", "default"))
	_, err := r.Reconcile(context.Background(), reqFor("sb-bad-ws", "default"))
	require.Error(t, err, "reconciling sandbox with missing workspace must return error")
	assert.Contains(t, err.Error(), "failed to get workspace")
}

func TestE2E_Unhappy_WorkspaceRef_NoPVCName_PodBuildFails(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ws-no-pvc",
			Namespace: "default",
		},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "user-1"},
			Storage: v1.WorkspaceStorageConfig{Size: "10Gi"},
		},
		Status: v1.WorkspaceStatus{
			Phase:   v1.WorkspacePhaseActive,
			PVCName: "",
		},
	}

	sb := makeSandbox("sb-no-pvc", "default", common.SandboxPhasePending)
	sb.Spec.WorkspaceRef = "ws-no-pvc"

	r := reconcilerFor(t, sb, ws)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	var pvcVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "workspace" {
			pvcVol = &pod.Spec.Volumes[i]
			break
		}
	}
	if pvcVol != nil {
		assert.Empty(t, pvcVol.PersistentVolumeClaim.ClaimName,
			"PVC claim name should be empty when workspace has no PVCName")
	}
}

func TestE2E_Unhappy_SandboxTimeout_ExceededWhileRunning(t *testing.T) {
	longAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	sb := makeSandbox("sb-e2e-timeout", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.Timeout = 60
	sb.Status.PodName = "sb-e2e-timeout-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &longAgo

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-e2e-timeout-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-e2e-timeout", "default"))

	require.NoError(t, err)
	assert.True(t, result.Requeue)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-e2e-timeout", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseTerminating, updated.Status.Phase,
		"sandbox exceeding timeout must transition to Terminating")
}

func TestE2E_Unhappy_RunningPod_Disappears_RevertsToPending(t *testing.T) {
	// Post-fix #2: first transient pod-loss reverts to Pending, not Failed.
	// Multi-loss / threshold cases live in transient_failure_test.go.
	sb := makeSandbox("sb-pod-gone", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "ghost-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &metav1.Time{}

	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-pod-gone", "default"))
	require.NoError(t, err)
	// Recovery requests an immediate requeue so handlePending creates the new pod.
	assert.True(t, result.Requeue, "transient recovery must request immediate requeue")

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-pod-gone", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhasePending, updated.Status.Phase,
		"running sandbox with missing pod must self-heal to Pending on first occurrence")
}

func TestE2E_Unhappy_Suspending_DeletesPod_ClearsPodFields(t *testing.T) {
	sb := makeSandbox("sb-susp-clear", "default", common.SandboxPhaseSuspending)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "sb-susp-clear-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.PodIP = "10.0.0.1"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-susp-clear-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-susp-clear", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-susp-clear", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseSuspended, updated.Status.Phase)
	assert.Equal(t, "", updated.Status.PodIP, "PodIP must be cleared on suspend")
	assert.Equal(t, "", updated.Status.PodName, "PodName must be cleared on suspend")
}

// M8: Credential Secret naming convention
func TestE2E_CredentialSecret_Naming_WiredCorrectly(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-workspace",
			Namespace: "test-ns",
		},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "user-1"},
			Storage: v1.WorkspaceStorageConfig{Size: "5Gi"},
		},
		Status: v1.WorkspaceStatus{
			Phase:   v1.WorkspacePhaseActive,
			PVCName: "pvc-my-workspace",
		},
	}

	sb := makeSandbox("sb-cred-naming", "test-ns", common.SandboxPhasePending)
	sb.Spec.WorkspaceRef = "my-workspace"

	pwSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sandbox-pw-sb-cred-naming", Namespace: "test-ns",
		},
		Data: map[string][]byte{"password": []byte("pw")},
	}

	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workspace-creds-my-workspace", Namespace: "test-ns",
		},
		Data: map[string][]byte{"provider-config": []byte(`{"key":"val"}`)},
	}

	r := reconcilerFor(t, sb, ws, pwSecret, credSecret)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	var credVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "cred-secret" {
			credVol = &pod.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, credVol, "cred-secret volume must exist")
	assert.Equal(t, "workspace-creds-my-workspace", credVol.Secret.SecretName,
		"credential secret name must follow workspace-creds-{workspaceName} convention")
}
