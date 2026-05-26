package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func TestMapCredSecretToWorkspaces_MatchingPrefix(t *testing.T) {
	r := reconcilerFor(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-my-workspace",
			Namespace: "default",
		},
	}
	result := r.mapCredSecretToWorkspaces(context.Background(), secret)
	require.Len(t, result, 1)
	assert.Equal(t, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "my-workspace", Namespace: "default"},
	}, result[0])
}

func TestMapCredSecretToWorkspaces_NonMatchingPrefix(t *testing.T) {
	r := reconcilerFor(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-pw-my-workspace",
			Namespace: "default",
		},
	}
	result := r.mapCredSecretToWorkspaces(context.Background(), secret)
	assert.Nil(t, result)
}

func TestMapCredSecretToWorkspaces_NonSecretObject(t *testing.T) {
	r := reconcilerFor(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-pod",
			Namespace: "default",
		},
	}
	result := r.mapCredSecretToWorkspaces(context.Background(), pod)
	assert.Nil(t, result)
}

func TestMapCredSecretToWorkspaces_DeleteEvent(t *testing.T) {
	r := reconcilerFor(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workspace-creds-deleted-ws",
			Namespace:         "test-ns",
			DeletionTimestamp: &metav1.Time{},
		},
	}
	result := r.mapCredSecretToWorkspaces(context.Background(), secret)
	require.Len(t, result, 1)
	assert.Equal(t, "deleted-ws", result[0].Name)
	assert.Equal(t, "test-ns", result[0].Namespace)
}

func TestMapCredSecretToWorkspaces_ExactPrefix(t *testing.T) {
	r := reconcilerFor(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-",
			Namespace: "default",
		},
	}
	result := r.mapCredSecretToWorkspaces(context.Background(), secret)
	require.Len(t, result, 1)
	assert.Equal(t, "", result[0].Name)
}

func TestSecretWatch_Integration_CredentialChangeTriggersReconcile(t *testing.T) {
	scheme := testScheme(t)
	ws := makeWorkspace("ws-cred-test", "default", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	expectedPodName := podName("ws-cred-test", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-cred-test", "default")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod, pwSecret).
		WithStatusSubresource(&v1.Workspace{}).
		Build()

	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-ws-cred-test",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"provider-config": []byte(`{"apiKey":"test-key"}`),
		},
	}
	require.NoError(t, r.Create(context.Background(), credSecret))

	_, err := r.Reconcile(context.Background(), reqFor("ws-cred-test", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-cred-test", Namespace: "default"}, updated))
	assert.NotEmpty(t, updated.Status.CredentialSecretHash, "credential secret hash should be recorded after reconcile")
}

func TestSecretWatch_Integration_CredentialChangeDetected(t *testing.T) {
	scheme := testScheme(t)
	ws := makeWorkspace("ws-cred-change", "default", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	expectedPodName := podName("ws-cred-change", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-cred-change", "default")

	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workspace-creds-ws-cred-change",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"provider-config": []byte(`{"apiKey":"old-key"}`),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod, pwSecret, credSecret).
		WithStatusSubresource(&v1.Workspace{}).
		Build()

	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reqFor("ws-cred-change", "default"))
	require.NoError(t, err)

	afterFirst := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-cred-change", Namespace: "default"}, afterFirst))
	firstHash := afterFirst.Status.CredentialSecretHash
	assert.NotEmpty(t, firstHash, "hash should be recorded on first reconcile")

	existingCred := &corev1.Secret{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "workspace-creds-ws-cred-change", Namespace: "default"}, existingCred))
	existingCred.Data["provider-config"] = []byte(`{"apiKey":"new-key"}`)
	require.NoError(t, r.Update(context.Background(), existingCred))

	_, err = r.Reconcile(context.Background(), reqFor("ws-cred-change", "default"))
	require.NoError(t, err)

	afterChange := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-cred-change", Namespace: "default"}, afterChange))
	assert.Equal(t, v1.WorkspacePhaseCreating, afterChange.Status.Phase, "should restart pod on credential change")
	assert.Equal(t, int32(1), afterChange.Status.RestartCount)
}

func TestSecretWatch_Integration_NoCredentialSecret_NoRestart(t *testing.T) {
	scheme := testScheme(t)
	ws := makeWorkspace("ws-no-cred", "default", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	expectedPodName := podName("ws-no-cred", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-no-cred", "default")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod, pwSecret).
		WithStatusSubresource(&v1.Workspace{}).
		Build()

	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	result, err := r.Reconcile(context.Background(), reqFor("ws-no-cred", "default"))
	require.NoError(t, err)
	assert.Equal(t, requeueActive, result.RequeueAfter, "should requeue normally when no cred secret")

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-no-cred", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseActive, updated.Status.Phase, "should stay Active")
	assert.Empty(t, updated.Status.CredentialSecretHash, "hash should be empty when no cred secret")
}
