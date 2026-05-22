package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	"github.com/lenaxia/llmsafespace/controller/internal/resources"
)

// Pod must have a numeric RunAsUser at the pod level. Without this, kubelet
// rejects the pod because the runtime-base image uses USER sandbox (a name)
// and the containers set RunAsNonRoot=true, so kubelet can't pre-flight the
// non-root check by name. This is the actual end-to-end failure mode that
// motivated this code.
func TestBuildPodSecurityContext_DefaultsToUid1000(t *testing.T) {
	sb := makeSandbox("sb", "ns", common.SandboxPhasePending)

	psc := buildPodSecurityContext(sb)

	require.NotNil(t, psc, "pod must have a SecurityContext")
	require.NotNil(t, psc.RunAsUser, "RunAsUser must be set")
	assert.Equal(t, int64(1000), *psc.RunAsUser)
	require.NotNil(t, psc.RunAsGroup)
	assert.Equal(t, int64(1000), *psc.RunAsGroup)
	require.NotNil(t, psc.FSGroup, "FSGroup needed for the workspace PVC mount")
	assert.Equal(t, int64(1000), *psc.FSGroup)
}

// Sandbox.Spec.SecurityContext must override the defaults. Operators can use
// this to support runtime images built with a different uid.
func TestBuildPodSecurityContext_SpecOverridesDefault(t *testing.T) {
	sb := makeSandbox("sb", "ns", common.SandboxPhasePending)
	sb.Spec.SecurityContext = &resources.SecurityContext{
		RunAsUser:  2000,
		RunAsGroup: 2001,
	}

	psc := buildPodSecurityContext(sb)

	assert.Equal(t, int64(2000), *psc.RunAsUser)
	assert.Equal(t, int64(2001), *psc.RunAsGroup)
	assert.Equal(t, int64(2001), *psc.FSGroup,
		"FSGroup tracks RunAsGroup so the workspace PVC is owned by the runtime user's group")
}

// Whole-pod assertion: the rendered pod has SecurityContext on PodSpec, not
// just on individual containers. This is the structural property kubelet
// checks. Tests at the buildPodSecurityContext level only verify the
// helper; this verifies the wiring into the pod.
func TestBuildSandboxPod_AppliesPodSecurityContext(t *testing.T) {
	sb := makeSandbox("sb-podsec", "default", common.SandboxPhasePending)
	r := reconcilerFor(t, sb)

	pod, err := r.buildSandboxPodWithContext(context.Background(), sb)
	require.NoError(t, err)

	require.NotNil(t, pod.Spec.SecurityContext, "pod-level SecurityContext required")
	require.NotNil(t, pod.Spec.SecurityContext.RunAsUser)
	assert.Equal(t, int64(1000), *pod.Spec.SecurityContext.RunAsUser)
}
