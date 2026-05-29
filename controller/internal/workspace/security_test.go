package workspace

// Security regression tests for Epic 17 sandbox pod hardening.
//
// G17 regression: ensure sandbox pods explicitly set
// AutomountServiceAccountToken=false. K8s defaults this to true, which
// would mount a default-namespace ServiceAccount token at
// /var/run/secrets/kubernetes.io/serviceaccount/token — readable by any
// process inside the pod. A compromised agent in the sandbox should never
// have a path to the K8s API.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func newWorkspaceForSecurity(t *testing.T) *v1.Workspace {
	t.Helper()
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ws-sec-regression",
			Namespace: "default",
		},
		Spec: v1.WorkspaceSpec{
			// Use an explicit image reference so resolveRuntimeImage doesn't
			// require a RuntimeEnvironment CRD in the fake client.
			Runtime: "ghcr.io/lenaxia/llmsafespace/runtimes/base:test",
		},
		Status: v1.WorkspaceStatus{
			PVCName: "pvc-sec-regression",
		},
	}
}

// TestG17_SandboxPodDoesNotAutomountSAToken is the headline regression for G17.
// Pre-fix the field was unset → kubelet defaults to true → SA token mounted.
// Post-fix the field is explicitly false → no token mount.
func TestG17_SandboxPodDoesNotAutomountSAToken(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)
	require.NotNil(t, pod, "buildPod must not return nil pod")

	require.NotNil(t, pod.Spec.AutomountServiceAccountToken,
		"AutomountServiceAccountToken must be explicitly set, not relying on default (which is true)")
	require.False(t, *pod.Spec.AutomountServiceAccountToken,
		"AutomountServiceAccountToken must be false on sandbox pods (G17)")
}

// TestSandboxPod_SecurityContextHardening locks in the existing security
// context guarantees so a future refactor can't silently weaken them.
func TestSandboxPod_SecurityContextHardening(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)
	require.NotNil(t, pod)
	require.NotEmpty(t, pod.Spec.Containers, "pod must have at least one container")

	main := pod.Spec.Containers[0]
	require.NotNil(t, main.SecurityContext, "main container must have SecurityContext")

	require.NotNil(t, main.SecurityContext.ReadOnlyRootFilesystem)
	require.True(t, *main.SecurityContext.ReadOnlyRootFilesystem,
		"main container must have ReadOnlyRootFilesystem=true")

	require.NotNil(t, main.SecurityContext.RunAsNonRoot)
	require.True(t, *main.SecurityContext.RunAsNonRoot,
		"main container must have RunAsNonRoot=true")

	require.NotNil(t, main.SecurityContext.AllowPrivilegeEscalation)
	require.False(t, *main.SecurityContext.AllowPrivilegeEscalation,
		"main container must have AllowPrivilegeEscalation=false")

	require.NotNil(t, main.SecurityContext.Capabilities)
	require.Contains(t, main.SecurityContext.Capabilities.Drop, corev1.Capability("ALL"),
		"main container must drop ALL capabilities")
}

// TestSandboxPod_VolumeFootprint locks in the volume mount inventory.
// Any new mount widens the attack surface and must be added to the threat
// model and to this list deliberately.
func TestSandboxPod_VolumeFootprint(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	expectedVolumes := map[string]bool{
		"workspace":    false,
		"sandbox-cfg":  false,
		"tmp":          false,
		"sandbox-home": false,
		"pw-secret":    false,
	}
	for _, v := range pod.Spec.Volumes {
		if _, ok := expectedVolumes[v.Name]; ok {
			expectedVolumes[v.Name] = true
		}
	}
	for name, found := range expectedVolumes {
		require.True(t, found, "expected sandbox volume %q to be present", name)
	}

	require.NotEmpty(t, pod.Spec.Containers)
	main := pod.Spec.Containers[0]

	expectedMounts := map[string]bool{
		"workspace":    false,
		"sandbox-cfg":  false,
		"tmp":          false,
		"sandbox-home": false,
	}
	for _, m := range main.VolumeMounts {
		if _, ok := expectedMounts[m.Name]; ok {
			expectedMounts[m.Name] = true
		}
	}
	for name, found := range expectedMounts {
		require.True(t, found, "expected main container mount %q to be present", name)
	}
}
