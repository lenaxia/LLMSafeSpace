package sandbox

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

// SandboxReconciler reconciles a Sandbox object
type SandboxReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile handles the reconciliation loop for Sandbox resources
func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", req.NamespacedName)
	logger.Info("Reconciling Sandbox")

	sandbox := &resources.Sandbox{}
	err := r.Get(ctx, req.NamespacedName, sandbox)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Sandbox resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Sandbox")
		return ctrl.Result{}, err
	}

	if !sandbox.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, sandbox)
	}

	if common.AddFinalizer(sandbox, common.SandboxFinalizer) {
		if err := r.Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox with finalizer")
			return ctrl.Result{}, err
		}
	}

	switch sandbox.Status.Phase {
	case "", common.SandboxPhasePending:
		return r.handlePendingSandbox(ctx, sandbox)
	case common.SandboxPhaseCreating:
		return r.handleCreatingSandbox(ctx, sandbox)
	case common.SandboxPhaseRunning:
		return r.handleRunningSandbox(ctx, sandbox)
	case common.SandboxPhaseSuspending:
		return r.handleSuspendingSandbox(ctx, sandbox)
	case common.SandboxPhaseResuming:
		return r.handleResumingSandbox(ctx, sandbox)
	case common.SandboxPhaseTerminating:
		return r.handleTerminatingSandbox(ctx, sandbox)
	case common.SandboxPhaseTerminated, common.SandboxPhaseFailed, common.SandboxPhaseSuspended:
		return ctrl.Result{}, nil
	default:
		logger.Info("Unknown sandbox phase", "phase", sandbox.Status.Phase)
		return ctrl.Result{}, nil
	}
}

func (r *SandboxReconciler) handlePendingSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling pending sandbox")

	sandbox.Status.Phase = common.SandboxPhaseCreating
	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status to Creating")
		return ctrl.Result{}, err
	}

	return r.createSandboxPod(ctx, sandbox)
}

func (r *SandboxReconciler) handleCreatingSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling creating sandbox")

	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod not found, reverting to pending")
			sandbox.Status.Phase = common.SandboxPhasePending
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Pending")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	if pod.Status.Phase == corev1.PodRunning {
		sandbox.Status.Phase = common.SandboxPhaseRunning
		sandbox.Status.StartTime = &metav1.Time{Time: time.Now()}
		sandbox.Status.PodIP = pod.Status.PodIP
		sandbox.Status.Endpoint = fmt.Sprintf("%s.%s.svc.cluster.local", pod.Name, pod.Namespace)

		conditions := []resources.SandboxCondition{}
		common.SetSandboxCondition(&conditions, common.ConditionPodRunning, "True", common.ReasonPodRunning, "Pod is running")
		common.SetSandboxCondition(&conditions, common.ConditionReady, "True", common.ReasonPodRunning, "Sandbox is ready")
		sandbox.Status.Conditions = conditions

		if err := r.Status().Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox status to Running")
			return ctrl.Result{}, err
		}

		logger.Info("Sandbox is now running")
		return ctrl.Result{}, nil
	}

	logger.Info("Pod is not running yet", "podPhase", pod.Status.Phase)
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *SandboxReconciler) handleRunningSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling running sandbox")

	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod not found, marking sandbox as failed")
			sandbox.Status.Phase = common.SandboxPhaseFailed

			conditions := sandbox.Status.Conditions
			common.SetSandboxCondition(&conditions, common.ConditionPodRunning, "False", common.ReasonPodNotRunning, "Pod not found")
			common.SetSandboxCondition(&conditions, common.ConditionReady, "False", common.ReasonPodNotRunning, "Sandbox failed")
			sandbox.Status.Conditions = conditions

			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Failed")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	if pod.Status.Phase != corev1.PodRunning {
		logger.Info("Pod is not running", "podPhase", pod.Status.Phase)
		sandbox.Status.Phase = common.SandboxPhaseFailed

		conditions := sandbox.Status.Conditions
		common.SetSandboxCondition(&conditions, common.ConditionPodRunning, "False", common.ReasonPodNotRunning, fmt.Sprintf("Pod is %s", pod.Status.Phase))
		common.SetSandboxCondition(&conditions, common.ConditionReady, "False", common.ReasonPodNotRunning, "Sandbox failed")
		sandbox.Status.Conditions = conditions

		if err := r.Status().Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox status to Failed")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if sandbox.Spec.Timeout > 0 && sandbox.Status.StartTime != nil {
		timeout := time.Duration(sandbox.Spec.Timeout) * time.Second
		if time.Since(sandbox.Status.StartTime.Time) > timeout {
			logger.Info("Sandbox has exceeded its timeout, terminating")
			sandbox.Status.Phase = common.SandboxPhaseTerminating
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminating")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}

	if pod.Status.Phase == corev1.PodRunning {
		if err := r.Status().Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox resource usage")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *SandboxReconciler) handleSuspendingSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling suspending sandbox")

	if sandbox.Status.PodName != "" {
		pod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
		if err == nil {
			logger.Info("Deleting pod for suspension", "pod", pod.Name)
			if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
				logger.Error(err, "Failed to delete Pod")
				return ctrl.Result{}, err
			}
		} else if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to get Pod")
			return ctrl.Result{}, err
		}
	}

	sandbox.Status.Phase = common.SandboxPhaseSuspended
	sandbox.Status.PodIP = ""
	sandbox.Status.PodName = ""
	sandbox.Status.PodNamespace = ""
	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status to Suspended")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SandboxReconciler) handleResumingSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling resuming sandbox")

	sandbox.Status.Phase = common.SandboxPhaseCreating
	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status to Creating")
		return ctrl.Result{}, err
	}

	return r.createSandboxPod(ctx, sandbox)
}

func (r *SandboxReconciler) handleTerminatingSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling terminating sandbox")

	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod not found, marking sandbox as terminated")
			sandbox.Status.Phase = common.SandboxPhaseTerminated
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminated")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	logger.Info("Deleting pod", "pod", pod.Name)
	if err := r.Delete(ctx, pod); err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete Pod")
			return ctrl.Result{}, err
		}
	}

	err = r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod deleted, marking sandbox as terminated")
			sandbox.Status.Phase = common.SandboxPhaseTerminated
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminated")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	logger.Info("Pod is still being deleted")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *SandboxReconciler) handleDeletion(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling sandbox deletion")

	if controllerutil.ContainsFinalizer(sandbox, common.SandboxFinalizer) {
		if sandbox.Status.Phase != common.SandboxPhaseTerminating &&
			sandbox.Status.Phase != common.SandboxPhaseTerminated &&
			sandbox.Status.Phase != common.SandboxPhaseFailed {
			sandbox.Status.Phase = common.SandboxPhaseTerminating
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminating")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}

		if sandbox.Status.PodName != "" {
			pod := &corev1.Pod{}
			err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
			if err == nil {
				logger.Info("Deleting pod", "pod", pod.Name)
				if err := r.Delete(ctx, pod); err != nil {
					if !errors.IsNotFound(err) {
						logger.Error(err, "Failed to delete Pod")
						return ctrl.Result{}, err
					}
				}
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			} else if !errors.IsNotFound(err) {
				logger.Error(err, "Failed to get Pod")
				return ctrl.Result{}, err
			}
		}

		controllerutil.RemoveFinalizer(sandbox, common.SandboxFinalizer)
		if err := r.Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to remove finalizer from Sandbox")
			return ctrl.Result{}, err
		}
	}

	logger.Info("Sandbox deletion handled successfully")
	return ctrl.Result{}, nil
}

func (r *SandboxReconciler) createSandboxPod(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Creating new pod for sandbox")

	if err := r.ensurePasswordSecret(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to ensure password secret")
		return ctrl.Result{}, err
	}

	pod, err := r.buildSandboxPodWithContext(ctx, sandbox)
	if err != nil {
		logger.Error(err, "Failed to build sandbox pod")
		return ctrl.Result{}, err
	}

	if err := controllerutil.SetControllerReference(sandbox, pod, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference on Pod")
		return ctrl.Result{}, err
	}

	if err := r.Create(ctx, pod); err != nil {
		logger.Error(err, "Failed to create Pod")
		return ctrl.Result{}, err
	}

	sandbox.Status.PodName = pod.Name
	sandbox.Status.PodNamespace = pod.Namespace

	conditions := []resources.SandboxCondition{}
	common.SetSandboxCondition(&conditions, common.ConditionPodCreated, "True", common.ReasonPodCreated, "Pod created successfully")
	sandbox.Status.Conditions = conditions

	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status with pod information")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// ensurePasswordSecret creates the sandbox password secret if it does not exist.
func (r *SandboxReconciler) ensurePasswordSecret(ctx context.Context, sandbox *resources.Sandbox) error {
	secretName := fmt.Sprintf("sandbox-pw-%s", sandbox.Name)
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: sandbox.Namespace}, secret)
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return err
	}

	password := common.GenerateRandomString(32)
	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: sandbox.Namespace,
		},
		Data: map[string][]byte{
			"password": []byte(password),
		},
	}
	if err := controllerutil.SetControllerReference(sandbox, newSecret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on password secret: %w", err)
	}
	return r.Create(ctx, newSecret)
}

// buildSandboxPodWithContext builds a sandbox pod, looking up workspace details if needed.
func (r *SandboxReconciler) buildSandboxPodWithContext(ctx context.Context, sandbox *resources.Sandbox) (*corev1.Pod, error) {
	podName := fmt.Sprintf("%s-%s", sandbox.Name, sandbox.UID[0:8])

	labels := map[string]string{
		common.LabelApp:       "llmsafespace",
		common.LabelComponent: common.ComponentSandbox,
		common.LabelSandboxID: sandbox.Name,
		common.LabelRuntime:   sandbox.Spec.Runtime,
	}

	annotations := map[string]string{
		common.AnnotationCreatedBy: common.ControllerName,
		common.AnnotationSandboxID: sandbox.Name,
	}

	runtimeImage := sandbox.Spec.Runtime

	trueVal := true
	falseVal := false

	mainContainer := corev1.Container{
		Name:    "sandbox",
		Image:   runtimeImage,
		Command: []string{"/usr/local/bin/entrypoint-opencode.sh"},
		Ports: []corev1.ContainerPort{
			{ContainerPort: 4096, Name: "opencode", Protocol: corev1.ProtocolTCP},
		},
		Env: []corev1.EnvVar{
			{Name: "SANDBOX_ID", Value: sandbox.Name},
			{Name: "WORKSPACE_DIR", Value: "/workspace"},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "sandbox-cfg", MountPath: "/sandbox-cfg", ReadOnly: true},
			{Name: "tmp", MountPath: "/tmp"},
			{Name: "sandbox-home", MountPath: "/home/sandbox"},
		},
	}

	volumes := []corev1.Volume{
		{Name: "sandbox-cfg", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "sandbox-home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}

	var initContainers []corev1.Container

	if sandbox.Spec.WorkspaceRef != "" {
		ws := &resources.Workspace{}
		if err := r.Get(ctx, client.ObjectKey{Name: sandbox.Spec.WorkspaceRef, Namespace: sandbox.Namespace}, ws); err != nil {
			return nil, fmt.Errorf("failed to get workspace %s: %w", sandbox.Spec.WorkspaceRef, err)
		}

		volumes = append(volumes, corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: ws.Status.PVCName,
				},
			},
		})
		mainContainer.VolumeMounts = append(mainContainer.VolumeMounts,
			corev1.VolumeMount{Name: "workspace", MountPath: "/workspace"})

		if len(ws.Spec.Packages) > 0 || ws.Spec.InitScript != "" {
			setupInit := r.buildWorkspaceSetupInit(ws, runtimeImage)
			initContainers = append(initContainers, setupInit)
		}
	}

	credInit, pwVolume, credVolume, err := r.buildCredentialSetupInit(ctx, sandbox, runtimeImage)
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
			Name:        podName,
			Namespace:   sandbox.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			InitContainers: initContainers,
			Containers:     []corev1.Container{mainContainer},
			Volumes:        volumes,
		},
	}

	return pod, nil
}

// buildCredentialSetupInit builds the credential-setup init container and the
// pw-secret projected volume it needs. Returns the container, the pw-secret volume,
// and an optional cred-secret volume (non-nil only when workspace credentials exist).
func (r *SandboxReconciler) buildCredentialSetupInit(ctx context.Context, sandbox *resources.Sandbox, runtimeImage string) (corev1.Container, corev1.Volume, *corev1.Volume, error) {
	credScript := `
if [ -f /mnt/secrets/credentials/provider-config ]; then
  cp /mnt/secrets/credentials/provider-config /sandbox-cfg/credentials
else
  echo '{}' > /sandbox-cfg/credentials
fi
cp /mnt/secrets/password/password /sandbox-cfg/password
`

	pwSecretName := fmt.Sprintf("sandbox-pw-%s", sandbox.Name)

	pwVolume := corev1.Volume{
		Name: "pw-secret",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: pwSecretName,
			},
		},
	}

	credInitMounts := []corev1.VolumeMount{
		{Name: "sandbox-cfg", MountPath: "/sandbox-cfg"},
		{Name: "pw-secret", MountPath: "/mnt/secrets/password", ReadOnly: true},
	}

	var credVolume *corev1.Volume

	if sandbox.Spec.WorkspaceRef != "" {
		credsSecretName := fmt.Sprintf("workspace-creds-%s", sandbox.Spec.WorkspaceRef)
		credSecret := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{Name: credsSecretName, Namespace: sandbox.Namespace}, credSecret)
		if err == nil {
			v := corev1.Volume{
				Name: "cred-secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: credsSecretName,
					},
				},
			}
			credVolume = &v
			credInitMounts = append(credInitMounts, corev1.VolumeMount{
				Name:      "cred-secret",
				MountPath: "/mnt/secrets/credentials",
				ReadOnly:  true,
			})
		} else if !errors.IsNotFound(err) {
			return corev1.Container{}, corev1.Volume{}, nil, fmt.Errorf("failed to check credentials secret: %w", err)
		}
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
		VolumeMounts: credInitMounts,
	}

	return credInit, pwVolume, credVolume, nil
}

// buildWorkspaceSetupInit builds the workspace-setup init container that installs
// packages and/or runs the initScript before the main container starts.
func (r *SandboxReconciler) buildWorkspaceSetupInit(ws *resources.Workspace, runtimeImage string) corev1.Container {
	trueVal := true
	falseVal := false

	return corev1.Container{
		Name:    "workspace-setup",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", buildWorkspaceSetupScript(ws)},
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

// buildWorkspaceSetupScript constructs the shell script for the workspace-setup init container.
func buildWorkspaceSetupScript(ws *resources.Workspace) string {
	script := "#!/bin/sh\nset -e\nmkdir -p /workspace/packages\n"

	for _, pkgSet := range ws.Spec.Packages {
		if len(pkgSet.Requirements) > 0 {
			args := ""
			for _, req := range pkgSet.Requirements {
				args += " " + req
			}
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

// SetupWithManager sets up the controller with the Manager
func (r *SandboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resources.Sandbox{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
