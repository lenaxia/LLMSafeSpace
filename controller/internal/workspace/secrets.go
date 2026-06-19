package workspace

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespaces/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

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

// cleanupFailedWorkspaceSecrets deletes the per-workspace K8s Secrets
// (`workspace-creds-*`, `workspace-pw-*`, `workspace-secrets-*`) when a
// workspace has entered the Failed phase. The Secrets are useless once
// the workspace is non-recoverable; leaving them behind is the symptom
// of Bug 12 in worklog 0085 (Secrets persisting 45+ hours after pod
// disappeared). Each Get is best-effort: missing-Secret is the desired
// end state.
func (r *WorkspaceReconciler) cleanupFailedWorkspaceSecrets(ctx context.Context, workspace *v1.Workspace) {
	logger := log.FromContext(ctx)
	for _, secretName := range []string{
		fmt.Sprintf("workspace-secrets-%s", workspace.Name),
		fmt.Sprintf("workspace-creds-%s", workspace.Name),
		fmt.Sprintf("workspace-pw-%s", workspace.Name),
	} {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: workspace.Namespace}, secret); err != nil {
			continue // already gone or not found
		}
		if err := r.Delete(ctx, secret); err != nil {
			logger.V(1).Info("Failed to delete Secret for Failed workspace",
				"name", secretName, "error", err.Error())
		}
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
