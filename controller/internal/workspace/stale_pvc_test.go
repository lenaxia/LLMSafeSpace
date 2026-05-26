package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func makeUnboundPVC(name, namespace string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")}},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
}

func TestIsPVCStale_Terminating(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-stale", "default", v1.WorkspacePhasePending)
	ws.UID = "ws-uid-1"
	now := metav1.Now()
	pvc := makeUnboundPVC("workspace-ws-stale", "default")
	pvc.DeletionTimestamp = &now
	pvc.OwnerReferences = []metav1.OwnerReference{{UID: ws.UID}}
	assert.True(t, r.isPVCStale(pvc, ws), "terminating PVC should be stale regardless of owner UID")
}

func TestIsPVCStale_OwnerMismatch(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-stale", "default", v1.WorkspacePhasePending)
	ws.UID = "ws-uid-1"
	pvc := makeUnboundPVC("workspace-ws-stale", "default")
	pvc.OwnerReferences = []metav1.OwnerReference{{UID: "different-uid"}}
	assert.True(t, r.isPVCStale(pvc, ws), "PVC with different owner UID should be stale")
}

func TestIsPVCStale_SameOwner(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-ok", "default", v1.WorkspacePhasePending)
	ws.UID = "ws-uid-1"
	pvc := makeUnboundPVC("workspace-ws-ok", "default")
	pvc.OwnerReferences = []metav1.OwnerReference{{UID: ws.UID}}
	assert.False(t, r.isPVCStale(pvc, ws), "PVC with matching owner UID should not be stale")
}

func TestIsPVCStale_NoOwner(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-noown", "default", v1.WorkspacePhasePending)
	pvc := makeUnboundPVC("workspace-ws-noown", "default")
	assert.False(t, r.isPVCStale(pvc, ws), "PVC without owner refs should not be treated as stale")
}

func TestReconcile_Pending_StalePVC_OwnerUIDMismatch_DeletesAndRecreates(t *testing.T) {
	ws := makeWorkspace("ws-mismatch", "default", v1.WorkspacePhasePending)
	ws.UID = "ws-uid-NEW"
	ws.Finalizers = []string{WorkspaceFinalizer}

	stalePVC := makeUnboundPVC("workspace-ws-mismatch", "default")
	stalePVC.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "llmsafespace.dev/v1",
		Kind:       "Workspace",
		Name:       "ws-mismatch",
		UID:        "ws-uid-OLD-DIFFERENT",
	}}

	r := reconcilerFor(t, ws, stalePVC)

	result, err := r.Reconcile(context.Background(), reqFor("ws-mismatch", "default"))
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{RequeueAfter: requeueCreating}, result)

	newPVC := &corev1.PersistentVolumeClaim{}
	pvcErr := r.Get(context.Background(), types.NamespacedName{Name: "workspace-ws-mismatch", Namespace: "default"}, newPVC)
	assert.NoError(t, pvcErr, "new PVC should be created after stale one is deleted")
	for _, ref := range newPVC.OwnerReferences {
		assert.Equal(t, types.UID("ws-uid-NEW"), ref.UID, "new PVC should reference current workspace UID")
	}

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-mismatch", Namespace: "default"}, updated))
	assert.Equal(t, "workspace-ws-mismatch", updated.Status.PVCName)
}

func TestReconcile_Pending_PVCWithoutOwnerRef_NotTreatedAsStale(t *testing.T) {
	ws := makeWorkspace("ws-adopted", "default", v1.WorkspacePhasePending)
	ws.UID = "ws-uid-1"
	ws.Finalizers = []string{WorkspaceFinalizer}

	pvc := makeBoundPVC("workspace-ws-adopted", "default", "")

	r := reconcilerFor(t, ws, pvc)

	_, err := r.Reconcile(context.Background(), reqFor("ws-adopted", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-adopted", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseCreating, updated.Status.Phase,
		"PVC without owner-ref must be treated as legitimate; transitions to Creating")
	_ = corev1.ClaimBound
}
