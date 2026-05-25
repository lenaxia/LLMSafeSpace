package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/handler"
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
		// Check for stale PVC from previous workspace generation.
		if r.isPVCStale(existingPVC, workspace) {
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

	// Pod exists — check if running.
	if existingPod.Status.Phase == corev1.PodRunning && existingPod.Status.PodIP != "" {
		now := metav1.Now()
		workspace.Status.Phase = v1.WorkspacePhaseActive
		workspace.Status.PodName = existingPod.Name
		workspace.Status.PodNamespace = existingPod.Namespace
		workspace.Status.PodIP = existingPod.Status.PodIP
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

	// Check credential secret change.
	if changed, newHash := r.credentialSecretChanged(ctx, workspace); changed {
		logger.Info("Credential secret changed; restarting pod")
		r.deletePodByName(ctx, name, workspace.Namespace)
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		workspace.Status.CredentialSecretHash = newHash
		workspace.Status.RestartCount++
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
		// Pod exists but not running — transient recovery.
		return r.recoverFromTransientPodLoss(ctx, workspace)
	}

	// Pod running — check timeout.
	if workspace.Spec.Timeout > 0 && workspace.Status.StartTime != nil {
		elapsed := time.Since(workspace.Status.StartTime.Time)
		if elapsed > time.Duration(workspace.Spec.Timeout)*time.Second {
			logger.Info("Pod timeout exceeded; suspending")
			workspace.Status.Phase = v1.WorkspacePhaseSuspending
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
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

// --- Credential secret change detection ---

func (r *WorkspaceReconciler) credentialSecretChanged(ctx context.Context, workspace *v1.Workspace) (bool, string) {
	credsName := fmt.Sprintf("workspace-creds-%s", workspace.Name)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: credsName, Namespace: workspace.Namespace}, secret); err != nil {
		return false, ""
	}
	newHash := hashSecretData(secret.Data)
	if workspace.Status.CredentialSecretHash == "" {
		// First time seeing credentials — store hash, no restart needed.
		workspace.Status.CredentialSecretHash = newHash
		return false, newHash
	}
	return newHash != workspace.Status.CredentialSecretHash, newHash
}

func hashSecretData(data map[string][]byte) string {
	h := sha256.New()
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(data[k])
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// --- Pod management helpers ---

func (r *WorkspaceReconciler) deletePodByName(ctx context.Context, name, namespace string) {
	pod := &corev1.Pod{}
	pod.Name = name
	pod.Namespace = namespace
	_ = r.Delete(ctx, pod)
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
			{ContainerPort: 4096, Name: "opencode", Protocol: corev1.ProtocolTCP},
		},
		Env: []corev1.EnvVar{
			{Name: "WORKSPACE_ID", Value: workspace.Name},
			{Name: "WORKSPACE_DIR", Value: "/workspace"},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/global/health", Port: intstr.FromInt(4096), Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 5, PeriodSeconds: 10, TimeoutSeconds: 3, FailureThreshold: 3,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/global/health", Port: intstr.FromInt(4096), Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 15, PeriodSeconds: 30, TimeoutSeconds: 5, FailureThreshold: 3,
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
	credInit, pwVolume, credVolume, err := r.buildCredentialSetupInit(ctx, workspace, runtimeImage)
	if err != nil {
		return nil, err
	}
	initContainers = append(initContainers, credInit)
	volumes = append(volumes, pwVolume)
	if credVolume != nil {
		volumes = append(volumes, *credVolume)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   workspace.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			InitContainers:  initContainers,
			Containers:      []corev1.Container{mainContainer},
			Volumes:         volumes,
			SecurityContext: buildPodSecurityContext(workspace),
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

func (r *WorkspaceReconciler) buildCredentialSetupInit(ctx context.Context, workspace *v1.Workspace, runtimeImage string) (corev1.Container, corev1.Volume, *corev1.Volume, error) {
	credScript := `
if [ -f /mnt/secrets/credentials/provider-config ]; then
  cp /mnt/secrets/credentials/provider-config /sandbox-cfg/credentials
else
  echo '{}' > /sandbox-cfg/credentials
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

	var credVolume *corev1.Volume
	credsSecretName := fmt.Sprintf("workspace-creds-%s", workspace.Name)
	credSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: credsSecretName, Namespace: workspace.Namespace}, credSecret); err == nil {
		v := corev1.Volume{
			Name: "cred-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: credsSecretName},
			},
		}
		credVolume = &v
		credMounts = append(credMounts, corev1.VolumeMount{
			Name: "cred-secret", MountPath: "/mnt/secrets/credentials", ReadOnly: true,
		})
	} else if !errors.IsNotFound(err) {
		return corev1.Container{}, corev1.Volume{}, nil, fmt.Errorf("checking credentials secret: %w", err)
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
	return credInit, pwVolume, credVolume, nil
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
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapCredSecretToWorkspaces)).
		Complete(r)
}

func (r *WorkspaceReconciler) mapCredSecretToWorkspaces(ctx context.Context, obj client.Object) []ctrl.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}
	const prefix = "workspace-creds-"
	if !strings.HasPrefix(secret.Name, prefix) {
		return nil
	}
	workspaceName := secret.Name[len(prefix):]
	return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: workspaceName, Namespace: secret.Namespace}}}
}

// sanitizeLabelValue replaces characters invalid in K8s label values.
func sanitizeLabelValue(s string) string {
	return strings.ReplaceAll(s, ":", "_")
}
