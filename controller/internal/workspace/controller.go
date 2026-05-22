package workspace

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	"github.com/lenaxia/llmsafespace/controller/internal/resources"
)

const WorkspaceFinalizer = "workspace.llmsafespace.dev/finalizer"

const pendingPhaseTimeout = 5 * time.Minute

type WorkspaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", req.NamespacedName)
	logger.Info("Reconciling Workspace")

	workspace := &resources.Workspace{}
	if err := r.Get(ctx, req.NamespacedName, workspace); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Workspace resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Workspace")
		return ctrl.Result{}, err
	}

	if !workspace.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, workspace)
	}

	switch workspace.Status.Phase {
	case "", resources.WorkspacePhasePending:
		return r.handlePending(ctx, workspace)
	case resources.WorkspacePhaseActive:
		return r.handleActive(ctx, workspace)
	case resources.WorkspacePhaseSuspending:
		return r.handleSuspending(ctx, workspace)
	case resources.WorkspacePhaseSuspended:
		return r.handleSuspended(ctx, workspace)
	case resources.WorkspacePhaseResuming:
		return r.handleResuming(ctx, workspace)
	case resources.WorkspacePhaseTerminating:
		return r.handleTerminating(ctx, workspace)
	case resources.WorkspacePhaseFailed:
		logger.Info("Workspace is in Failed phase; manual intervention required", "workspace", workspace.Name)
		return ctrl.Result{}, nil
	default:
		logger.Info("Unknown workspace phase", "phase", workspace.Status.Phase)
		return ctrl.Result{}, nil
	}
}

func (r *WorkspaceReconciler) handlePending(ctx context.Context, workspace *resources.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace})
	logger.Info("Handling pending workspace")

	if common.AddFinalizer(workspace, WorkspaceFinalizer) {
		if err := r.Update(ctx, workspace); err != nil {
			logger.Error(err, "Failed to add finalizer to Workspace")
			return ctrl.Result{}, err
		}
	}

	pvcName := fmt.Sprintf("workspace-%s", workspace.Name)

	existingPVC := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: workspace.Namespace}, existingPVC)
	if err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to get PVC")
			return ctrl.Result{}, err
		}
		if !workspace.CreationTimestamp.IsZero() && time.Since(workspace.CreationTimestamp.Time) > pendingPhaseTimeout {
			workspace.Status.Phase = resources.WorkspacePhaseFailed
			workspace.Status.Message = "workspace timed out in Pending phase after 5 minutes"
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
		newPVC := r.buildPVC(workspace, pvcName)
		if setErr := controllerutil.SetControllerReference(workspace, newPVC, r.Scheme); setErr != nil {
			logger.Error(setErr, "Failed to set controller reference on PVC")
			return ctrl.Result{}, setErr
		}
		if createErr := r.Create(ctx, newPVC); createErr != nil {
			logger.Error(createErr, "Failed to create PVC")
			return ctrl.Result{}, createErr
		}
		logger.Info("PVC created", "pvc", pvcName)
		workspace.Status.PVCName = pvcName
		if err := r.Status().Update(ctx, workspace); err != nil {
			logger.Error(err, "Failed to update Workspace status with PVCName")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if existingPVC.Status.Phase != corev1.ClaimBound {
		if !workspace.CreationTimestamp.IsZero() && time.Since(workspace.CreationTimestamp.Time) > pendingPhaseTimeout {
			workspace.Status.Phase = resources.WorkspacePhaseFailed
			workspace.Status.Message = "PVC not bound after 5 minutes"
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	workspace.Status.PVCName = pvcName
	workspace.Status.Phase = resources.WorkspacePhaseActive
	if err := r.Status().Update(ctx, workspace); err != nil {
		logger.Error(err, "Failed to update Workspace status to Active")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WorkspaceReconciler) buildPVC(workspace *resources.Workspace, pvcName string) *corev1.PersistentVolumeClaim {
	accessMode := corev1.ReadWriteOnce
	if workspace.Spec.Storage.AccessMode == "ReadWriteMany" {
		accessMode = corev1.ReadWriteMany
	}

	storageSize := resource.MustParse(workspace.Spec.Storage.Size)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: workspace.Namespace,
			Labels: map[string]string{
				"app":                        "llmsafespace",
				"llmsafespace.dev/workspace": workspace.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageSize,
				},
			},
		},
	}

	if workspace.Spec.Storage.StorageClassName != "" {
		sc := workspace.Spec.Storage.StorageClassName
		pvc.Spec.StorageClassName = &sc
	}

	return pvc
}

func (r *WorkspaceReconciler) handleActive(ctx context.Context, workspace *resources.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace})
	logger.Info("Handling active workspace")

	sandboxes, err := r.listSandboxesForWorkspace(ctx, workspace)
	if err != nil {
		logger.Error(err, "Failed to list Sandboxes for Workspace")
		return ctrl.Result{}, err
	}

	workspace.Status.ActiveSessions = int32(len(sandboxes))
	if err := r.Status().Update(ctx, workspace); err != nil {
		logger.Error(err, "Failed to update Workspace active sessions")
		return ctrl.Result{}, err
	}

	if workspace.Spec.AutoSuspend == nil || !workspace.Spec.AutoSuspend.Enabled || workspace.Spec.AutoSuspend.IdleTimeoutSeconds <= 0 {
		return ctrl.Result{}, nil
	}

	idleTimeout := time.Duration(workspace.Spec.AutoSuspend.IdleTimeoutSeconds) * time.Second

	if workspace.Status.LastActivityAt == nil {
		workspace.Status.Phase = resources.WorkspacePhaseSuspending
		if err := r.Status().Update(ctx, workspace); err != nil {
			logger.Error(err, "Failed to update Workspace status to Suspending")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	lastActivity := workspace.Status.LastActivityAt.Time
	elapsed := time.Since(lastActivity)
	if elapsed >= idleTimeout {
		workspace.Status.Phase = resources.WorkspacePhaseSuspending
		if err := r.Status().Update(ctx, workspace); err != nil {
			logger.Error(err, "Failed to update Workspace status to Suspending")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	nextCheckAt := lastActivity.Add(time.Duration(float64(idleTimeout) * 0.8))
	requeueAfter := time.Until(nextCheckAt)
	if requeueAfter < time.Second {
		requeueAfter = time.Second
	}

	logger.Info("Workspace not yet idle, requeuing", "requeueAfter", requeueAfter)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *WorkspaceReconciler) handleSuspending(ctx context.Context, workspace *resources.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace})
	logger.Info("Handling suspending workspace")

	if workspace.Spec.AutoSuspend != nil && workspace.Spec.AutoSuspend.Enabled &&
		workspace.Spec.AutoSuspend.IdleTimeoutSeconds > 0 &&
		workspace.Status.LastActivityAt != nil {

		idleTimeout := time.Duration(workspace.Spec.AutoSuspend.IdleTimeoutSeconds) * time.Second
		if time.Since(workspace.Status.LastActivityAt.Time) < idleTimeout {
			logger.Info("Recent activity detected during suspend; reverting to Active")
			workspace.Status.Phase = resources.WorkspacePhaseActive
			if err := r.Status().Update(ctx, workspace); err != nil {
				logger.Error(err, "Failed to revert Workspace status to Active")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	if err := r.deleteWorkspacePods(ctx, workspace); err != nil {
		logger.Error(err, "Failed to delete workspace pods")
		return ctrl.Result{}, err
	}

	if err := r.updateSandboxesToSuspended(ctx, workspace); err != nil {
		logger.Error(err, "Failed to update Sandbox CRDs to Suspended")
		return ctrl.Result{}, err
	}

	workspace.Status.Phase = resources.WorkspacePhaseSuspended
	if err := r.Status().Update(ctx, workspace); err != nil {
		logger.Error(err, "Failed to update Workspace status to Suspended")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WorkspaceReconciler) handleSuspended(ctx context.Context, workspace *resources.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace})
	logger.Info("Handling suspended workspace")

	if workspace.Spec.TTLSecondsAfterSuspended <= 0 {
		return ctrl.Result{}, nil
	}

	ttl := time.Duration(workspace.Spec.TTLSecondsAfterSuspended) * time.Second

	var referenceTime time.Time
	if workspace.Status.LastActivityAt != nil {
		referenceTime = workspace.Status.LastActivityAt.Time
	} else {
		referenceTime = workspace.CreationTimestamp.Time
	}

	elapsed := time.Since(referenceTime)
	if elapsed >= ttl {
		workspace.Status.Phase = resources.WorkspacePhaseTerminating
		if err := r.Status().Update(ctx, workspace); err != nil {
			logger.Error(err, "Failed to update Workspace status to Terminating")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	remaining := ttl - elapsed
	logger.Info("Workspace TTL not expired, requeuing", "remainingTTL", remaining)
	return ctrl.Result{RequeueAfter: remaining}, nil
}

func (r *WorkspaceReconciler) handleResuming(ctx context.Context, workspace *resources.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace})
	logger.Info("Handling resuming workspace")

	sandboxes, err := r.listSandboxesForWorkspace(ctx, workspace)
	if err != nil {
		logger.Error(err, "Failed to list Sandboxes for Workspace")
		return ctrl.Result{}, err
	}

	allRunning := true
	for i := range sandboxes {
		if sandboxes[i].Status.Phase != common.SandboxPhaseRunning {
			allRunning = false
			break
		}
	}

	if allRunning {
		workspace.Status.Phase = resources.WorkspacePhaseActive
		if err := r.Status().Update(ctx, workspace); err != nil {
			logger.Error(err, "Failed to update Workspace status to Active")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *WorkspaceReconciler) handleTerminating(ctx context.Context, workspace *resources.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace})
	logger.Info("Handling terminating workspace")

	pvcName := workspace.Status.PVCName
	if pvcName == "" {
		pvcName = fmt.Sprintf("workspace-%s", workspace.Name)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: workspace.Namespace}, pvc)
	if err == nil {
		if deleteErr := r.Delete(ctx, pvc); deleteErr != nil && !errors.IsNotFound(deleteErr) {
			logger.Error(deleteErr, "Failed to delete PVC")
			return ctrl.Result{}, deleteErr
		}
	} else if !errors.IsNotFound(err) {
		logger.Error(err, "Failed to get PVC for deletion")
		return ctrl.Result{}, err
	}

	if err := r.deleteSandboxCRDs(ctx, workspace); err != nil {
		logger.Error(err, "Failed to delete Sandbox CRDs")
		return ctrl.Result{}, err
	}

	workspace.Status.Phase = resources.WorkspacePhaseTerminated
	if err := r.Status().Update(ctx, workspace); err != nil {
		logger.Error(err, "Failed to update Workspace status to Terminated")
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(workspace, WorkspaceFinalizer)
	if err := r.Update(ctx, workspace); err != nil {
		logger.Error(err, "Failed to remove finalizer from Workspace")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WorkspaceReconciler) handleDeletion(ctx context.Context, workspace *resources.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace})
	logger.Info("Handling workspace deletion")

	if !controllerutil.ContainsFinalizer(workspace, WorkspaceFinalizer) {
		return ctrl.Result{}, nil
	}

	if err := r.deleteSandboxCRDs(ctx, workspace); err != nil {
		logger.Error(err, "Failed to delete Sandbox CRDs during deletion")
		return ctrl.Result{}, err
	}

	pvcName := workspace.Status.PVCName
	if pvcName == "" {
		pvcName = fmt.Sprintf("workspace-%s", workspace.Name)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: workspace.Namespace}, pvc)
	if err == nil {
		if deleteErr := r.Delete(ctx, pvc); deleteErr != nil && !errors.IsNotFound(deleteErr) {
			logger.Error(deleteErr, "Failed to delete PVC during workspace deletion")
			return ctrl.Result{}, deleteErr
		}
	} else if !errors.IsNotFound(err) {
		logger.Error(err, "Failed to get PVC during workspace deletion")
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(workspace, WorkspaceFinalizer)
	if err := r.Update(ctx, workspace); err != nil {
		logger.Error(err, "Failed to remove finalizer from Workspace")
		return ctrl.Result{}, err
	}

	logger.Info("Workspace deletion handled successfully")
	return ctrl.Result{}, nil
}

func (r *WorkspaceReconciler) listSandboxesForWorkspace(ctx context.Context, workspace *resources.Workspace) ([]resources.Sandbox, error) {
	sandboxList := &resources.SandboxList{}
	if err := r.List(ctx, sandboxList,
		client.InNamespace(workspace.Namespace),
		client.MatchingLabels{"llmsafespace.dev/workspace": workspace.Name},
	); err != nil {
		return nil, err
	}
	return sandboxList.Items, nil
}

func (r *WorkspaceReconciler) updateSandboxesToSuspended(ctx context.Context, workspace *resources.Workspace) error {
	sandboxes, err := r.listSandboxesForWorkspace(ctx, workspace)
	if err != nil {
		return err
	}

	for i := range sandboxes {
		sb := &sandboxes[i]
		sb.Status.Phase = common.SandboxPhaseSuspended
		if updateErr := r.Status().Update(ctx, sb); updateErr != nil {
			return updateErr
		}
	}
	return nil
}

func (r *WorkspaceReconciler) deleteSandboxCRDs(ctx context.Context, workspace *resources.Workspace) error {
	logger := log.FromContext(ctx).WithValues("workspace", types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace})

	sandboxes, err := r.listSandboxesForWorkspace(ctx, workspace)
	if err != nil {
		return err
	}

	for i := range sandboxes {
		sb := &sandboxes[i]
		logger.Info("Deleting Sandbox CRD", "sandbox", sb.Name)
		if deleteErr := r.Delete(ctx, sb); deleteErr != nil && !errors.IsNotFound(deleteErr) {
			return deleteErr
		}
	}
	return nil
}

func (r *WorkspaceReconciler) deleteWorkspacePods(ctx context.Context, workspace *resources.Workspace) error {
	logger := log.FromContext(ctx).WithValues("workspace", types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace})

	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(workspace.Namespace),
		client.MatchingLabels{"llmsafespace.dev/workspace": workspace.Name},
	); err != nil {
		return err
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		logger.Info("Deleting pod", "pod", pod.Name)
		if deleteErr := r.Delete(ctx, pod); deleteErr != nil && !errors.IsNotFound(deleteErr) {
			return deleteErr
		}
	}
	return nil
}

func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resources.Workspace{}).
		Complete(r)
}
