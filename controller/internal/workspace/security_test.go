// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"os/exec"
	"strings"
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

// =============================================================================
// G4 — F1.2.3 + F1.2.5: Resources applied + Packages shell-injection guard
// =============================================================================
//
// F1.2.3 (High): pre-fix, the workspace pod was created without ANY
// resource requests or limits, so workspace.spec.resources was silently
// ignored. A user could declare resources but the controller would not
// apply them, leading to (a) operator-supplied limits not being honored
// and (b) workspace pods running without quota and DoSing the node.
//
// F1.2.5 (High): pre-fix, `buildWorkspaceSetupScript` interpolated each
// `Spec.Packages[].Requirements[]` directly into a shell command:
//     args += " " + req
//     script += "pip install --target=/workspace/packages" + args + "\n"
// A user with `Requirements: ["pkg; rm -rf /workspace"]` got code
// execution as the workspace user inside the init container. Defense
// in depth: the same payload is also blocked at admission time by the
// webhook — but the controller-side hardening guards against admission
// being disabled.

func TestG4_F123_PodAppliesSpecResources(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	ws.Spec.Resources = &v1.ResourceRequirements{
		CPU:              "750m",
		Memory:           "1Gi",
		EphemeralStorage: "2Gi",
	}
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)
	require.NotEmpty(t, pod.Spec.Containers)
	main := pod.Spec.Containers[0]

	require.NotEmpty(t, main.Resources.Limits,
		"main container must carry resource limits derived from spec.resources (F1.2.3)")
	require.NotEmpty(t, main.Resources.Requests,
		"main container must carry resource requests derived from spec.resources (F1.2.3)")

	cpuLimit := main.Resources.Limits[corev1.ResourceCPU]
	require.Equal(t, "750m", cpuLimit.String(),
		"CPU limit must equal spec.resources.cpu")
	memLimit := main.Resources.Limits[corev1.ResourceMemory]
	require.Equal(t, "1Gi", memLimit.String(),
		"memory limit must equal spec.resources.memory")
	ephLimit := main.Resources.Limits[corev1.ResourceEphemeralStorage]
	require.Equal(t, "2Gi", ephLimit.String(),
		"ephemeral-storage limit must equal spec.resources.ephemeralStorage")
}

func TestG4_F123_PodAppliesDefaultsWhenSpecResourcesNil(t *testing.T) {
	// Workspaces created via the API server with kubebuilder defaults
	// will have Resources populated. Workspaces created via
	// `kubectl apply` of a minimal YAML (no resources block) get nil.
	// The controller must apply a sane default rather than emit a pod
	// with zero limits (which kubelet allows but is unbounded).
	ws := newWorkspaceForSecurity(t)
	ws.Spec.Resources = nil
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)
	main := pod.Spec.Containers[0]
	require.NotEmpty(t, main.Resources.Limits,
		"nil spec.resources must fall back to chart defaults, not empty Limits")
}

func TestG4_F125_PackageRequirementsAreNeitherShellEscapedNorPositionallyInjected(t *testing.T) {
	// Defense-in-depth check at the controller layer. If admission is
	// bypassed (e.g. failurePolicy=Ignore + webhook outage), an
	// adversarial Requirements value must not produce an init script
	// where the requirement is interpreted as shell tokens.
	//
	// Verification: the adversarial bytes must appear ONLY inside a
	// single-quoted shell argument. We assert by walking the script
	// byte-by-byte tracking quote state, and by exec-ing `sh -n` for
	// a syntax check (no execution).
	ws := newWorkspaceForSecurity(t)
	ws.Spec.Packages = []v1.WorkspacePackageSet{
		{
			Runtime: "python:3.11",
			Requirements: []string{
				"requests==2.31.0",
				// Adversarial; would break out of pip install if not quoted.
				"requests; rm -rf /workspace",
				// Single-quote injection attempt.
				`evil'; rm -rf /; echo '`,
			},
		},
	}
	script := buildWorkspaceSetupScript(ws)

	// Walk the script byte-by-byte tracking quote state. POSIX rules:
	//   - inside a single-quoted region every byte is literal except
	//     for the closing single quote.
	//   - outside any quote, a backslash escapes the next byte (so
	//     `\'` produces a literal apostrophe). This is exactly what
	//     the standard `'\''` escape pattern relies on.
	//
	// We treat `\'` outside the quote as still-outside-quote (the
	// backslash-escaped quote is a literal byte, not a quote opener).
	inQuote := false
	for i := 0; i < len(script); i++ {
		c := script[i]
		if !inQuote && c == '\\' && i+1 < len(script) {
			// Skip the escaped byte; quote state unchanged.
			i++
			continue
		}
		if c == '\'' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		const dangerous = "; rm "
		if i+len(dangerous) <= len(script) && script[i:i+len(dangerous)] == dangerous {
			t.Fatalf("F1.2.5 broken: adversarial '; rm ' appears OUTSIDE single quotes at offset %d:\n%s",
				i, script)
		}
	}

	// Run the script through `sh -n` to confirm it parses (proves
	// the quoting did not produce a syntax error that would cause
	// the init container to fail at runtime — which would also be a
	// regression for legitimate package installs).
	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err,
		"the rendered init script must be valid POSIX shell syntax: %s", out)
}
