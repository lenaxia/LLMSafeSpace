package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func TestCopyDefaultCredentials_NoDefault_DoesNothing(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-no-default", "default", v1.WorkspacePhaseCreating)
	r.copyDefaultCredentials(context.Background(), ws)

	secret := &corev1.Secret{}
	err := r.Get(context.Background(), types.NamespacedName{
		Name: "workspace-creds-ws-no-default", Namespace: "default",
	}, secret)
	assert.True(t, err != nil, "should not create secret when no default exists")
}

func TestCopyDefaultCredentials_DefaultExists_CreatesCopy(t *testing.T) {
	defaultSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-default",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"provider-config": []byte(`{"providers":{"openai":{"apiKey":"sk-test123"}}}`),
		},
	}
	r := reconcilerFor(t, defaultSecret)
	ws := makeWorkspace("ws-copy", "default", v1.WorkspacePhaseCreating)

	err := r.copyDefaultCredentials(context.Background(), ws)
	require.NoError(t, err)

	copied := &corev1.Secret{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{
		Name: "workspace-creds-ws-copy", Namespace: "default",
	}, copied))

	assert.Equal(t, []byte(`{"providers":{"openai":{"apiKey":"sk-test123"}}}`),
		copied.Data["provider-config"])
	assert.Equal(t, "Workspace", copied.OwnerReferences[0].Kind)
	assert.Equal(t, "ws-copy", copied.OwnerReferences[0].Name)
}

func TestCopyDefaultCredentials_WorkspaceSecretAlreadyExists_Skips(t *testing.T) {
	defaultSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-default",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"provider-config": []byte(`{"providers":{"openai":{"apiKey":"default-key"}}}`),
		},
	}
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-ws-exists",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"provider-config": []byte(`{"providers":{"openai":{"apiKey":"custom-key"}}}`),
		},
	}
	r := reconcilerFor(t, defaultSecret, existingSecret)
	ws := makeWorkspace("ws-exists", "default", v1.WorkspacePhaseCreating)

	err := r.copyDefaultCredentials(context.Background(), ws)
	require.NoError(t, err)

	existing := &corev1.Secret{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{
		Name: "workspace-creds-ws-exists", Namespace: "default",
	}, existing))
	assert.Equal(t, []byte(`{"providers":{"openai":{"apiKey":"custom-key"}}}`),
		existing.Data["provider-config"], "should not overwrite existing secret")
}

func TestCopyDefaultCredentials_DefaultExistsButEmptyData_CreatesEmpty(t *testing.T) {
	defaultSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-default",
			Namespace: "default",
		},
		Data: map[string][]byte{},
	}
	r := reconcilerFor(t, defaultSecret)
	ws := makeWorkspace("ws-empty-default", "default", v1.WorkspacePhaseCreating)

	err := r.copyDefaultCredentials(context.Background(), ws)
	require.NoError(t, err)

	copied := &corev1.Secret{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{
		Name: "workspace-creds-ws-empty-default", Namespace: "default",
	}, copied))
	assert.Empty(t, copied.Data["provider-config"])
}
