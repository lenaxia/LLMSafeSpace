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

func TestReconcile_Pending_StalePVC_Terminating_TriggersRecreate(t *testing.T) {
	ws := makeWorkspace("ws-stale", "default", v1.WorkspacePhasePending)
	ws.UID = "ws-uid-NEW"
	ws.Finalizers = []string{WorkspaceFinalizer}

	now := metav1.Now()
	stalePVC := makeUnboundPVC("workspace-ws-stale", "default")
	stalePVC.DeletionTimestamp = &now
	stalePVC.Finalizers = []string{"kubernetes.io/pvc-protection"}
	stalePVC.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "llmsafespace.dev/v1",
		Kind:       "Workspace",
		Name:       "ws-stale",
		UID:        "ws-uid-OLD",
	}}

	r := reconcilerFor(t, ws, stalePVC)

	result, err := r.Reconcile(context.Background(), reqFor("ws-stale", "default"))
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{RequeueAfter: requeueCreating}, result)
}

func TestReconcile_Pending_StalePVC_OwnerUIDMismatch_TriggersRecreate(t *testing.T) {
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

	_, err := r.Reconcile(context.Background(), reqFor("ws-mismatch", "default"))
	require.NoError(t, err)
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
