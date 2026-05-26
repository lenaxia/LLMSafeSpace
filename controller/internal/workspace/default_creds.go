package workspace

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

const defaultCredentialSecretName = "workspace-creds-default"

func (r *WorkspaceReconciler) copyDefaultCredentials(ctx context.Context, workspace *v1.Workspace) error {
	credsName := fmt.Sprintf("workspace-creds-%s", workspace.Name)
	existing := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: credsName, Namespace: workspace.Namespace}, existing); err == nil {
		return nil
	} else if !errors.IsNotFound(err) {
		return err
	}

	defaultSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: defaultCredentialSecretName, Namespace: workspace.Namespace}, defaultSecret); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	copy := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      credsName,
			Namespace: workspace.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "llmsafespace.dev/v1",
					Kind:       "Workspace",
					Name:       workspace.Name,
					UID:        workspace.UID,
				},
			},
		},
		Data: defaultSecret.Data,
	}
	return r.Create(ctx, copy)
}
