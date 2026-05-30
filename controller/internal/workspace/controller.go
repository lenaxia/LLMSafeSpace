package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/agentd"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

type WorkspaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", req.NamespacedName)

	workspace := &v1.Workspace{}
	if err := r.Get(ctx, req.NamespacedName, workspace); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !workspace.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, workspace)
	}

	switch workspace.Status.Phase {
	case "", v1.WorkspacePhasePending:
		return r.handlePending(ctx, workspace)
	case v1.WorkspacePhaseCreating:
		return r.handleCreating(ctx, workspace)
	case v1.WorkspacePhaseActive:
		return r.handleActive(ctx, workspace)
	case v1.WorkspacePhaseSuspending:
		return r.handleSuspending(ctx, workspace)
	case v1.WorkspacePhaseSuspended:
		return r.handleSuspended(ctx, workspace)
	case v1.WorkspacePhaseResuming:
		return r.handleResuming(ctx, workspace)
	case v1.WorkspacePhaseTerminating:
		return r.handleTerminating(ctx, workspace)
	case v1.WorkspacePhaseFailed:
		logger.Info("Workspace in Failed phase; manual intervention required")
		return ctrl.Result{}, nil
	default:
		logger.Info("Unknown workspace phase", "phase", workspace.Status.Phase)
		return ctrl.Result{}, nil
	}
}

func (r *WorkspaceReconciler) handlePending(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if common.AddFinalizer(workspace, WorkspaceFinalizer) {
		if err := r.Update(ctx, workspace); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Ensure PVC.
	pvcName := fmt.Sprintf("workspace-%s", workspace.Name)
	existingPVC := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: workspace.Namespace}, existingPVC)

	if err == nil {
		if r.isPVCStale(existingPVC, workspace) {
			logger.Info("Deleting stale PVC", "pvc", pvcName, "reason", "owner mismatch or terminating")
			if delErr := r.Delete(ctx, existingPVC); delErr != nil {
				return ctrl.Result{}, delErr
			}
			err = errors.NewNotFound(corev1.Resource("persistentvolumeclaims"), pvcName)
		}
	}

	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if r.pendingTimedOut(workspace) {
			workspace.Status.Phase = v1.WorkspacePhaseFailed
			workspace.Status.Message = "workspace timed out in Pending phase"
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
		newPVC := r.buildPVC(workspace, pvcName)
		if err := controllerutil.SetControllerReference(workspace, newPVC, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, newPVC); err != nil {
			if errors.IsAlreadyExists(err) {
				return ctrl.Result{RequeueAfter: requeueCreating}, nil
			}
			return ctrl.Result{}, err
		}
		workspace.Status.PVCName = pvcName
		if err := r.Status().Update(ctx, workspace); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueCreating}, nil
	}

	// PVC exists — check if bound.
	if existingPVC.Status.Phase != corev1.ClaimBound {
		if r.pvcUsesWaitForFirstConsumer(ctx, existingPVC) {
			// WaitForFirstConsumer: PVC won't bind until pod mounts it.
			// Transition to Creating so pod gets created.
			workspace.Status.PVCName = pvcName
			workspace.Status.Phase = v1.WorkspacePhaseCreating
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
		if r.pendingTimedOut(workspace) {
			workspace.Status.Phase = v1.WorkspacePhaseFailed
			workspace.Status.Message = "PVC not bound after timeout"
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
		return ctrl.Result{RequeueAfter: requeueActive}, nil
	}

	// PVC bound — ensure password secret, then transition to Creating.
	if err := r.ensurePasswordSecret(ctx, workspace); err != nil {
		logger.Error(err, "Failed to ensure password secret")
		return ctrl.Result{}, err
	}

	workspace.Status.PVCName = pvcName
	workspace.Status.Phase = v1.WorkspacePhaseCreating
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}

func (r *WorkspaceReconciler) handleCreating(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	// Check if pod already exists.
	existingPod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: workspace.Namespace}, existingPod)
	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// Pod doesn't exist — create it.
		pod, buildErr := r.buildPod(ctx, workspace)
		if buildErr != nil {
			logger.Error(buildErr, "Failed to build pod")
			workspace.Status.Phase = v1.WorkspacePhaseFailed
			workspace.Status.Message = fmt.Sprintf("pod build failed: %v", buildErr)
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
		if err := controllerutil.SetControllerReference(workspace, pod, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, pod); err != nil {
			if errors.IsAlreadyExists(err) {
				return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
		workspace.Status.PodName = pod.Name
		workspace.Status.PodNamespace = pod.Namespace
		if err := r.Status().Update(ctx, workspace); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueCreating}, nil
	}

	// Delete ephemeral secrets as soon as init containers complete (minimize etcd exposure).
	if allInitContainersComplete(existingPod) {
		r.deleteEphemeralSecretsSecret(ctx, workspace)
	}

	// Pod exists — check if running.
	if existingPod.Status.Phase == corev1.PodRunning && existingPod.Status.PodIP != "" {
		now := metav1.Now()
		workspace.Status.Phase = v1.WorkspacePhaseActive
		workspace.Status.PodName = existingPod.Name
		workspace.Status.PodNamespace = existingPod.Namespace
		workspace.Status.PodIP = existingPod.Status.PodIP
		workspace.Status.ImageTag = imageTagFromPod(existingPod)
		workspace.Status.Endpoint = fmt.Sprintf("http://%s:4096", existingPod.Status.PodIP)
		workspace.Status.StartTime = &now
		workspace.Status.Message = ""
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	if existingPod.Status.Phase == corev1.PodFailed {
		workspace.Status.Phase = v1.WorkspacePhaseFailed
		workspace.Status.Message = "pod entered Failed phase during creation"
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	return ctrl.Result{RequeueAfter: requeueCreating}, nil
}

func (r *WorkspaceReconciler) handleActive(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	// Check restart generation.
	if workspace.Spec.RestartGeneration > workspace.Status.ObservedRestartGeneration {
		logger.Info("Restart generation bumped; deleting pod", "gen", workspace.Spec.RestartGeneration)
		r.deletePodByName(ctx, name, workspace.Namespace)
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		workspace.Status.RestartCount++
		workspace.Status.ObservedRestartGeneration = workspace.Spec.RestartGeneration
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	// Check pod exists and is running.
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: workspace.Namespace}, pod)
	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// Pod missing — transient recovery.
		return r.recoverFromTransientPodLoss(ctx, workspace)
	}

	if pod.Status.Phase != corev1.PodRunning {
		return r.recoverFromTransientPodLoss(ctx, workspace)
	}

	// Detect architecture drift: if the running pod's nodeSelector doesn't
	// match the desired architecture, delete the pod so it gets recreated
	// on a node with the correct arch. Skip if pod has no nodeSelector
	// (legacy pod created before multi-arch support).
	desiredArch := workspace.Spec.Architecture
	if desiredArch == "" {
		desiredArch = "amd64"
	}
	if pod.Spec.NodeSelector != nil && pod.Spec.NodeSelector["kubernetes.io/arch"] != desiredArch {
		logger.Info("Architecture changed; recreating pod", "desired", desiredArch, "current", pod.Spec.NodeSelector["kubernetes.io/arch"])
		r.deletePodByName(ctx, name, workspace.Namespace)
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return r.recoverFromTransientPodLoss(ctx, workspace)
		}
	}

	// Clean up ephemeral secrets Secret (safety net — should already be deleted in handleCreating).
	r.deleteEphemeralSecretsSecret(ctx, workspace)

	// Pod running — check timeout.
	if workspace.Spec.Timeout > 0 && workspace.Status.StartTime != nil {
		elapsed := time.Since(workspace.Status.StartTime.Time)
		if elapsed > time.Duration(workspace.Spec.Timeout)*time.Second {
			logger.Info("Pod timeout exceeded; suspending")
			workspace.Status.Phase = v1.WorkspacePhaseSuspending
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
	}

	if workspace.Status.PodIP != "" && workspace.Status.StartTime != nil {
		if workspace.Status.LastHealthCheckAt != nil && workspace.Status.LastHealthCheckAt.Before(workspace.Status.StartTime) {
			workspace.Status.ConsecutiveHealthFailures = 0
			workspace.Status.LastHealthCheckAt = nil
		}
	}

	// Check idle auto-suspend.
	if workspace.Spec.AutoSuspend != nil && workspace.Spec.AutoSuspend.Enabled {
		timeout := workspace.Spec.AutoSuspend.IdleTimeoutSeconds
		if timeout <= 0 {
			timeout = 86400
		}
		if workspace.Status.LastActivityAt != nil {
			idle := time.Since(workspace.Status.LastActivityAt.Time)
			if idle > time.Duration(timeout)*time.Second {
				logger.Info("Workspace idle timeout exceeded; suspending",
					"lastActivity", workspace.Status.LastActivityAt, "idle", idle, "timeout", time.Duration(timeout)*time.Second)
				workspace.Status.Phase = v1.WorkspacePhaseSuspending
				return ctrl.Result{}, r.Status().Update(ctx, workspace)
			}
		}
	}

	// Reset transient failure counter if stable long enough.
	r.maybeResetTransientCounter(workspace)
	// Check agent health (HTTP to daemon — rate-limited).
	r.checkAgentHealth(ctx, workspace)

	if err := r.Status().Update(ctx, workspace); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: requeueActive}, nil
}

func (r *WorkspaceReconciler) handleSuspending(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	r.deletePodByName(ctx, name, workspace.Namespace)

	now := metav1.Now()
	workspace.Status.Phase = v1.WorkspacePhaseSuspended
	workspace.Status.PodName = ""
	workspace.Status.PodNamespace = ""
	workspace.Status.PodIP = ""
	workspace.Status.Endpoint = ""
	workspace.Status.SuspendedAt = &now
	workspace.Status.TransientFailureCount = 0
	workspace.Status.Sessions = nil
	workspace.Status.ActiveSessions = 0
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}

func (r *WorkspaceReconciler) handleSuspended(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	if workspace.Spec.TTLSecondsAfterSuspended <= 0 || workspace.Status.SuspendedAt == nil {
		return ctrl.Result{}, nil
	}
	elapsed := time.Since(workspace.Status.SuspendedAt.Time)
	ttl := time.Duration(workspace.Spec.TTLSecondsAfterSuspended) * time.Second
	if elapsed >= ttl {
		workspace.Status.Phase = v1.WorkspacePhaseTerminating
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}
	return ctrl.Result{RequeueAfter: ttl - elapsed}, nil
}

func (r *WorkspaceReconciler) handleResuming(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	// Ensure password secret exists (may have been cleaned up).
	if err := r.ensurePasswordSecret(ctx, workspace); err != nil {
		return ctrl.Result{}, err
	}
	workspace.Status.Phase = v1.WorkspacePhaseCreating
	workspace.Status.SuspendedAt = nil
	// Reset idle clock: the workspace was idle before suspension, but the
	// resume action itself counts as user activity. Without this, handleActive
	// would compare LastActivityAt (pre-suspend) against now and immediately
	// re-suspend the workspace.
	now := metav1.Now()
	workspace.Status.LastActivityAt = &now
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}

func (r *WorkspaceReconciler) handleTerminating(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	// Delete pod.
	r.deletePodByName(ctx, name, workspace.Namespace)

	// Delete PVC.
	if workspace.Status.PVCName != "" {
		pvc := &corev1.PersistentVolumeClaim{}
		pvc.Name = workspace.Status.PVCName
		pvc.Namespace = workspace.Namespace
		if err := r.Delete(ctx, pvc); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	// Delete password secret.
	pwSecret := &corev1.Secret{}
	pwSecret.Name = passwordSecretName(workspace.Name)
	pwSecret.Namespace = workspace.Namespace
	if err := r.Delete(ctx, pwSecret); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	workspace.Status.Phase = v1.WorkspacePhaseTerminated
	workspace.Status.PodName = ""
	workspace.Status.PodIP = ""
	workspace.Status.Endpoint = ""
	workspace.Status.Sessions = nil
	workspace.Status.ActiveSessions = 0
	workspace.Status.DiskUsedBytes = 0
	workspace.Status.DiskTotalBytes = 0
	if err := r.Status().Update(ctx, workspace); err != nil {
		return ctrl.Result{}, err
	}

	common.RemoveFinalizer(workspace, WorkspaceFinalizer)
	return ctrl.Result{}, r.Update(ctx, workspace)
}

func (r *WorkspaceReconciler) handleDeletion(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(workspace, WorkspaceFinalizer) {
		return ctrl.Result{}, nil
	}
	// Reuse terminating logic.
	workspace.Status.Phase = v1.WorkspacePhaseTerminating
	return r.handleTerminating(ctx, workspace)
}

// --- Transient recovery ---

func (r *WorkspaceReconciler) recoverFromTransientPodLoss(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	workspace.Status.TransientFailureCount++
	now := metav1.Now()
	workspace.Status.LastTransientFailureAt = &now

	maxRetries := int32(MaxTransientFailures)
	if workspace.Spec.MaxRetries > 0 {
		maxRetries = workspace.Spec.MaxRetries
	}

	if workspace.Status.TransientFailureCount >= maxRetries {
		workspace.Status.Phase = v1.WorkspacePhaseFailed
		workspace.Status.Message = fmt.Sprintf("pod lost %d times; marking failed", workspace.Status.TransientFailureCount)
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	// Self-heal: revert to Creating.
	workspace.Status.Phase = v1.WorkspacePhaseCreating
	workspace.Status.PodIP = ""
	workspace.Status.Endpoint = ""
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}

func (r *WorkspaceReconciler) maybeResetTransientCounter(workspace *v1.Workspace) {
	if workspace.Status.TransientFailureCount == 0 {
		return
	}
	if workspace.Status.LastTransientFailureAt == nil {
		return
	}
	elapsed := time.Since(workspace.Status.LastTransientFailureAt.Time)
	if elapsed > time.Duration(TransientFailureResetWindow)*time.Second {
		workspace.Status.TransientFailureCount = 0
		workspace.Status.LastTransientFailureAt = nil
	}
}

// --- Pod management helpers ---

func (r *WorkspaceReconciler) deletePodByName(ctx context.Context, name, namespace string) {
	pod := &corev1.Pod{}
	pod.Name = name
	pod.Namespace = namespace
	_ = r.Delete(ctx, pod)
}

func (r *WorkspaceReconciler) deleteEphemeralSecretsSecret(ctx context.Context, workspace *v1.Workspace) {
	secretName := fmt.Sprintf("workspace-secrets-%s", workspace.Name)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: workspace.Namespace}, secret); err != nil {
		return // doesn't exist, nothing to do
	}
	if err := r.Delete(ctx, secret); err != nil {
		log.FromContext(ctx).V(1).Info("Failed to delete ephemeral secrets secret", "name", secretName, "error", err.Error())
	}
}

func allInitContainersComplete(pod *corev1.Pod) bool {
	if len(pod.Status.InitContainerStatuses) == 0 {
		return false
	}
	for _, s := range pod.Status.InitContainerStatuses {
		if s.State.Terminated == nil || s.State.Terminated.ExitCode != 0 {
			return false
		}
	}
	return true
}

func (r *WorkspaceReconciler) ensurePasswordSecret(ctx context.Context, workspace *v1.Workspace) error {
	name := passwordSecretName(workspace.Name)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: workspace.Namespace}, secret); err == nil {
		return nil
	}
	password := common.GenerateRandomString(32)
	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: workspace.Namespace,
		},
		Data: map[string][]byte{"password": []byte(password)},
	}
	if err := controllerutil.SetControllerReference(workspace, newSecret, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, newSecret)
}

// --- PVC helpers ---

func (r *WorkspaceReconciler) isPVCStale(pvc *corev1.PersistentVolumeClaim, workspace *v1.Workspace) bool {
	if !pvc.DeletionTimestamp.IsZero() {
		return true
	}
	if len(pvc.OwnerReferences) > 0 {
		for _, owner := range pvc.OwnerReferences {
			if owner.UID == workspace.UID {
				return false
			}
		}
		return true
	}
	return false
}

func (r *WorkspaceReconciler) pendingTimedOut(workspace *v1.Workspace) bool {
	return !workspace.CreationTimestamp.IsZero() && time.Since(workspace.CreationTimestamp.Time) > pendingPhaseTimeout
}

func (r *WorkspaceReconciler) pvcUsesWaitForFirstConsumer(ctx context.Context, pvc *corev1.PersistentVolumeClaim) bool {
	scName := ""
	if pvc.Spec.StorageClassName != nil {
		scName = *pvc.Spec.StorageClassName
	}
	if scName == "" {
		return false
	}
	sc := &storagev1.StorageClass{}
	if err := r.Get(ctx, types.NamespacedName{Name: scName}, sc); err != nil {
		return false
	}
	if sc.VolumeBindingMode == nil {
		return false
	}
	return *sc.VolumeBindingMode == storagev1.VolumeBindingWaitForFirstConsumer
}

func (r *WorkspaceReconciler) buildPVC(workspace *v1.Workspace, pvcName string) *corev1.PersistentVolumeClaim {
	storageSize := resource.MustParse(workspace.Spec.Storage.Size)
	accessMode := corev1.ReadWriteOnce
	if workspace.Spec.Storage.AccessMode == "ReadWriteMany" {
		accessMode = corev1.ReadWriteMany
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: workspace.Namespace,
			Labels: map[string]string{
				LabelApp:       AppName,
				LabelComponent: ComponentWorkspace,
				LabelWorkspace: workspace.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: storageSize},
			},
		},
	}
	if workspace.Spec.Storage.StorageClassName != "" {
		pvc.Spec.StorageClassName = &workspace.Spec.Storage.StorageClassName
	}
	return pvc
}

// --- Pod building ---

func (r *WorkspaceReconciler) buildPod(ctx context.Context, workspace *v1.Workspace) (*corev1.Pod, error) {
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	runtimeImage, runtimeEnvName, err := resolveRuntimeImage(ctx, r.Client, workspace.Spec.Runtime)
	if err != nil {
		return nil, fmt.Errorf("resolving runtime image: %w", err)
	}

	labels := map[string]string{
		LabelApp:       AppName,
		LabelComponent: ComponentWorkspace,
		LabelWorkspace: workspace.Name,
		LabelRuntime:   sanitizeLabelValue(workspace.Spec.Runtime),
	}

	annotations := map[string]string{
		"llmsafespace.dev/created-by": "controller",
	}
	if runtimeEnvName != "" {
		annotations["llmsafespace.dev/runtime-env"] = runtimeEnvName
	}

	trueVal := true
	falseVal := false

	mainContainer := corev1.Container{
		Name:    "workspace",
		Image:   runtimeImage,
		Command: []string{"/usr/local/bin/entrypoint-opencode.sh"},
		Ports: []corev1.ContainerPort{
			{ContainerPort: agentd.AgentPort, Name: "opencode", Protocol: corev1.ProtocolTCP},
			{ContainerPort: agentd.AgentdPort, Name: "agentd", Protocol: corev1.ProtocolTCP},
		},
		Env: []corev1.EnvVar{
			{Name: "WORKSPACE_ID", Value: workspace.Name},
			{Name: "WORKSPACE_DIR", Value: agentd.WorkspacePath},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/v1/readyz",
					Port: intstr.FromInt(agentd.AgentdPort),
				},
			},
			InitialDelaySeconds: 10, PeriodSeconds: 15, TimeoutSeconds: 3, FailureThreshold: 5,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/v1/healthz",
					Port: intstr.FromInt(agentd.AgentdPort),
				},
			},
			InitialDelaySeconds: 15, PeriodSeconds: 30, TimeoutSeconds: 5, FailureThreshold: 6,
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "sandbox-cfg", MountPath: "/sandbox-cfg", ReadOnly: true},
			{Name: "tmp", MountPath: "/tmp"},
			{Name: "sandbox-home", MountPath: "/home/sandbox"},
		},
	}

	volumes := []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: workspace.Status.PVCName},
		}},
		{Name: "sandbox-cfg", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "sandbox-home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}

	var initContainers []corev1.Container

	// Workspace setup init (packages + initScript).
	if len(workspace.Spec.Packages) > 0 || workspace.Spec.InitScript != "" {
		initContainers = append(initContainers, buildWorkspaceSetupInit(workspace, runtimeImage))
	}

	// Credential setup init.
	credInit, pwVolume, userSecretsVol, err := r.buildCredentialSetupInit(ctx, workspace, runtimeImage)
	if err != nil {
		return nil, err
	}
	initContainers = append(initContainers, credInit)
	volumes = append(volumes, pwVolume)
	if userSecretsVol != nil {
		volumes = append(volumes, *userSecretsVol)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   workspace.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			InitContainers: initContainers,
			Containers:     []corev1.Container{mainContainer},
			Volumes:        volumes,
			NodeSelector:   buildNodeSelector(workspace),
			// G17 (Epic 17): Sandbox pods MUST NOT automount the default
			// ServiceAccount token. The agent has no business calling the
			// K8s API; mounting the token only widens the blast radius for
			// a compromised sandbox. Without this, kubelet writes a JWT to
			// /var/run/secrets/kubernetes.io/serviceaccount/token that any
			// process inside the pod can read. See
			// `controller/internal/workspace/security_test.go` for the
			// regression that locks this in.
			AutomountServiceAccountToken: &falseVal,
			SecurityContext:              buildPodSecurityContext(workspace),
		},
	}
	return pod, nil
}

func buildPodSecurityContext(workspace *v1.Workspace) *corev1.PodSecurityContext {
	runAsUser := int64(1000)
	runAsGroup := int64(1000)
	if psc := workspace.Spec.PodSecurityContext; psc != nil {
		if psc.RunAsUser != 0 {
			runAsUser = psc.RunAsUser
		}
		if psc.RunAsGroup != 0 {
			runAsGroup = psc.RunAsGroup
		}
	}
	return &corev1.PodSecurityContext{
		RunAsUser:  &runAsUser,
		RunAsGroup: &runAsGroup,
		FSGroup:    &runAsGroup,
	}
}

func buildNodeSelector(workspace *v1.Workspace) map[string]string {
	arch := workspace.Spec.Architecture
	if arch == "" {
		arch = "amd64"
	}
	return map[string]string{
		"kubernetes.io/arch": arch,
	}
}

func (r *WorkspaceReconciler) buildCredentialSetupInit(ctx context.Context, workspace *v1.Workspace, runtimeImage string) (corev1.Container, corev1.Volume, *corev1.Volume, error) {
	credScript := `
if [ -f /mnt/secrets/user-secrets/secrets.json ]; then
  cp /mnt/secrets/user-secrets/secrets.json /sandbox-cfg/secrets.json
fi
cp /mnt/secrets/password/password /sandbox-cfg/password
`
	pwVolume := corev1.Volume{
		Name: "pw-secret",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: passwordSecretName(workspace.Name)},
		},
	}

	credMounts := []corev1.VolumeMount{
		{Name: "sandbox-cfg", MountPath: "/sandbox-cfg"},
		{Name: "pw-secret", MountPath: "/mnt/secrets/password", ReadOnly: true},
	}

	// Epic 10: mount user-secrets if the ephemeral Secret exists.
	userSecretsName := fmt.Sprintf("workspace-secrets-%s", workspace.Name)
	userSecretsSecret := &corev1.Secret{}
	var userSecretsVolume *corev1.Volume
	if err := r.Get(ctx, types.NamespacedName{Name: userSecretsName, Namespace: workspace.Namespace}, userSecretsSecret); err == nil {
		v := corev1.Volume{
			Name: "user-secrets",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: userSecretsName},
			},
		}
		userSecretsVolume = &v
		credMounts = append(credMounts, corev1.VolumeMount{
			Name: "user-secrets", MountPath: "/mnt/secrets/user-secrets", ReadOnly: true,
		})
	} else if !errors.IsNotFound(err) {
		return corev1.Container{}, corev1.Volume{}, nil, fmt.Errorf("checking user-secrets secret: %w", err)
	}

	trueVal := true
	falseVal := false
	credInit := corev1.Container{
		Name:    "credential-setup",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", credScript},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: credMounts,
	}
	return credInit, pwVolume, userSecretsVolume, nil
}

func buildWorkspaceSetupInit(workspace *v1.Workspace, runtimeImage string) corev1.Container {
	trueVal := true
	falseVal := false
	return corev1.Container{
		Name:    "workspace-setup",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", buildWorkspaceSetupScript(workspace)},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "tmp", MountPath: "/tmp"},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
}

func buildWorkspaceSetupScript(ws *v1.Workspace) string {
	script := "#!/bin/sh\nset -e\nmkdir -p /workspace/packages\n"
	for _, pkgSet := range ws.Spec.Packages {
		if len(pkgSet.Requirements) == 0 {
			continue
		}
		args := ""
		for _, req := range pkgSet.Requirements {
			args += " " + req
		}
		rt := pkgSet.Runtime
		switch {
		case len(rt) >= 6 && rt[:6] == "nodejs":
			script += "cd /workspace/packages && npm install" + args + "\n"
		case len(rt) >= 2 && rt[:2] == "go":
			for _, req := range pkgSet.Requirements {
				script += "cd /workspace/packages && go install " + req + "\n"
			}
		default:
			script += "pip install --target=/workspace/packages" + args + "\n"
		}
	}
	if ws.Spec.InitScript != "" {
		script += "cat > /tmp/init-script.sh << 'INITSCRIPT'\n"
		script += ws.Spec.InitScript + "\n"
		script += "INITSCRIPT\n"
		script += "chmod +x /tmp/init-script.sh\n"
		script += "/tmp/init-script.sh\n"
	}
	return script
}

// --- Setup ---

func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Workspace{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

// sanitizeLabelValue replaces characters invalid in K8s label values.
func sanitizeLabelValue(s string) string {
	return strings.ReplaceAll(s, ":", "_")
}

// imageTagFromPod extracts the image tag (portion after the last colon) from
// the first container's image reference. Returns the full image ref if no tag
// separator is found.
func imageTagFromPod(pod *corev1.Pod) string {
	if len(pod.Spec.Containers) == 0 {
		return ""
	}
	image := pod.Spec.Containers[0].Image
	if i := strings.LastIndex(image, ":"); i >= 0 {
		return image[i+1:]
	}
	return image
}

func (r *WorkspaceReconciler) setCondition(ws *v1.Workspace, condType v1.WorkspaceConditionType, status, reason, message string) {
	for i := range ws.Status.Conditions {
		if ws.Status.Conditions[i].Type == condType {
			if ws.Status.Conditions[i].Status == status && ws.Status.Conditions[i].Reason == reason {
				ws.Status.Conditions[i].Message = message
				return
			}
			ws.Status.Conditions[i].Status = status
			ws.Status.Conditions[i].Reason = reason
			ws.Status.Conditions[i].Message = message
			ws.Status.Conditions[i].LastTransitionTime = metav1.Now()
			return
		}
	}
	ws.Status.Conditions = append(ws.Status.Conditions, v1.WorkspaceCondition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
}

var (
	healthCheckInterval         = 15 * time.Second
	healthCheckBackoffInterval  = 60 * time.Second
	healthCheckFailureThreshold = int32(3)
	healthCheckGracePeriod      = 30 * time.Second
	agentdPort                  = agentd.AgentdPort
)

var healthHTTPClient = &http.Client{Timeout: 5 * time.Second}

func (r *WorkspaceReconciler) shouldRunHealthCheck(ws *v1.Workspace) bool {
	if ws.Status.StartTime != nil && time.Since(ws.Status.StartTime.Time) < healthCheckGracePeriod {
		return false
	}
	interval := healthCheckInterval
	if ws.Status.ConsecutiveHealthFailures >= healthCheckFailureThreshold {
		interval = healthCheckBackoffInterval
	}
	if ws.Status.LastHealthCheckAt == nil {
		return true
	}
	return time.Since(ws.Status.LastHealthCheckAt.Time) >= interval
}

func (r *WorkspaceReconciler) checkAgentHealth(ctx context.Context, ws *v1.Workspace) {
	logger := log.FromContext(ctx)

	if ws.Status.PodIP != "" && ws.Status.StartTime != nil && ws.Status.LastHealthCheckAt != nil {
		if ws.Status.LastHealthCheckAt.Before(ws.Status.StartTime) {
			ws.Status.ConsecutiveHealthFailures = 0
			ws.Status.LastHealthCheckAt = nil
		}
	}

	if !r.shouldRunHealthCheck(ws) {
		return
	}
	if ws.Status.PodIP == "" {
		return
	}

	endpoint := fmt.Sprintf("http://%s:%d/v1/statusz", ws.Status.PodIP, agentdPort)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return
	}

	resp, err := healthHTTPClient.Do(req)

	now := metav1.Now()
	ws.Status.LastHealthCheckAt = &now

	if err != nil {
		ws.Status.ConsecutiveHealthFailures++
		r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "Unknown",
			v1.ReasonHealthCheckFailed, err.Error())
		return
	}
	defer resp.Body.Close()

	var status agentd.StatuszResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		ws.Status.ConsecutiveHealthFailures++
		r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "Unknown",
			v1.ReasonHealthCheckFailed, "failed to decode status response")
		return
	}

	if !status.Healthy {
		ws.Status.ConsecutiveHealthFailures++
		r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "False",
			v1.ReasonAgentUnhealthy, "agent process not responding")
		if ws.Status.ConsecutiveHealthFailures >= healthCheckFailureThreshold {
			podN := podName(ws.Name, string(ws.UID))
			logger.Info("Agent unhealthy beyond threshold; restarting pod",
				"failures", ws.Status.ConsecutiveHealthFailures, "pod", podN)
			r.deletePodByName(ctx, podN, ws.Namespace)
			ws.Status.Phase = v1.WorkspacePhaseCreating
			ws.Status.PodIP = ""
			ws.Status.Endpoint = ""
			ws.Status.RestartCount++
		}
		return
	}

	ws.Status.ConsecutiveHealthFailures = 0

	if !status.Ready || len(status.Connected) == 0 {
		r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "False",
			v1.ReasonAgentDegraded, fmt.Sprintf("no providers connected (configured=%d, connected=%v)",
				status.ProvidersConfigured, status.Connected))
		return
	}

	// Populate agent-reported metadata on CRD status.
	ws.Status.ActiveSessions = int32(status.SessionsActive)
	if len(status.Sessions) > 0 {
		sessions := make([]v1.AgentSessionStatus, len(status.Sessions))
		for i, s := range status.Sessions {
			sessions[i] = v1.AgentSessionStatus{ID: s.ID, Title: s.Title, Status: s.Status}
		}
		ws.Status.Sessions = sessions
	} else {
		ws.Status.Sessions = nil
	}
	if status.Disk != nil {
		ws.Status.DiskUsedBytes = status.Disk.UsedBytes
		ws.Status.DiskTotalBytes = status.Disk.TotalBytes
	}

	r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "True",
		v1.ReasonAgentHealthy, fmt.Sprintf("connected=%v sessions=%d version=%s",
			status.Connected, status.SessionsActive, status.AgentVersion))
}
