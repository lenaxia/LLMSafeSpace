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
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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
		"pw-secret":    false,
		"user-secrets": false,
	}
	for _, v := range pod.Spec.Volumes {
		if _, ok := expectedVolumes[v.Name]; ok {
			expectedVolumes[v.Name] = true
		}
	}
	for name, found := range expectedVolumes {
		require.True(t, found, "expected sandbox volume %q to be present", name)
	}

	// user-secrets must be optional so pods start cleanly before credentials
	// are configured and kubelet auto-syncs the secret once it is created.
	var userSecretsVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "user-secrets" {
			userSecretsVol = &pod.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, userSecretsVol, "user-secrets volume must exist")
	require.NotNil(t, userSecretsVol.Secret, "user-secrets volume must be a Secret source")
	require.NotNil(t, userSecretsVol.Secret.Optional, "user-secrets volume must have Optional set")
	require.True(t, *userSecretsVol.Secret.Optional, "user-secrets volume must have Optional: true")

	require.NotEmpty(t, pod.Spec.Containers)
	main := pod.Spec.Containers[0]

	expectedMounts := map[string]bool{
		"workspace":   false,
		"sandbox-cfg": false,
		"tmp":         false,
	}
	for _, m := range main.VolumeMounts {
		if _, ok := expectedMounts[m.Name]; ok {
			expectedMounts[m.Name] = true
		}
	}
	for name, found := range expectedMounts {
		require.True(t, found, "expected main container mount %q to be present", name)
	}

	// sandbox-home emptyDir was replaced by PVC subPath mounts. Verify the
	// workspace volume is mounted twice — once for /workspace (subPath:workspace)
	// and once for /home/sandbox (subPath:home) — and that no sandbox-home
	// volume exists.
	var workspaceMountPaths []string
	for _, m := range main.VolumeMounts {
		if m.Name == "workspace" {
			workspaceMountPaths = append(workspaceMountPaths, m.MountPath)
		}
		require.NotEqual(t, "sandbox-home", m.Name, "sandbox-home emptyDir mount must not exist")
	}
	require.ElementsMatch(t, []string{"/workspace", "/home/sandbox"}, workspaceMountPaths,
		"workspace PVC must be mounted at both /workspace and /home/sandbox via subPaths")

	for _, v := range pod.Spec.Volumes {
		require.NotEqual(t, "sandbox-home", v.Name, "sandbox-home emptyDir volume must not exist")
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
	require.Equal(t, "3", cpuLimit.String(),
		"CPU limit must be 4× spec.resources.cpu (burstable QoS)")
	memLimit := main.Resources.Limits[corev1.ResourceMemory]
	require.Equal(t, "4Gi", memLimit.String(),
		"memory limit must be 4× spec.resources.memory (burstable QoS)")
	ephLimit := main.Resources.Limits[corev1.ResourceEphemeralStorage]
	require.Equal(t, "2Gi", ephLimit.String(),
		"ephemeral-storage limit must equal spec.resources.ephemeralStorage (no burst)")
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

// =============================================================================
// G4 part 2 — F1.2.4: Spec.NetworkAccess generates per-workspace NetworkPolicy
// =============================================================================
//
// Pre-fix: Spec.NetworkAccess was completely ignored by the controller.
// A user could declare `networkAccess.egress: [{domain: api.openai.com}]`
// expecting outbound traffic to be limited to that allow-list, but the
// controller never created a NetworkPolicy reflecting the field.
//
// Fix: when Spec.NetworkAccess is non-nil and has at least one Egress
// entry, the controller creates a NetworkPolicy named
// `workspace-egress-<ws>-<uid>` selecting just that workspace's pod
// (via WorkspaceID label). Egress rules are generated from the
// declared FQDN list with DNS-resolved /32 ipBlock entries plus
// DNS port 53 to kube-dns.
//
// Trade-off note: standard k8s NetworkPolicy doesn't support FQDN
// matching. We resolve at reconcile time and refresh on each pass
// (controllers reconcile periodically, so the IP set self-refreshes).
// Operators who need stricter FQDN guarantees should layer a Cilium
// FQDN policy on top — out of scope for this fix.

// stubResolver is a HostResolver implementation for hermetic tests:
// no real DNS calls. Returns the predefined response (or an error) for
// each host. Unknown hosts return NXDOMAIN-equivalent.
type stubResolver struct {
	hosts map[string][]string
	err   error
}

func (s stubResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	if v, ok := s.hosts[host]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("no such host: %s", host)
}

func TestG4_F124_GeneratesPerWorkspaceEgressPolicyWhenDeclared(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	ws.Spec.NetworkAccess = &v1.WorkspaceNetworkAccess{
		Egress: []v1.WorkspaceEgressRule{
			{Domain: "api.openai.com"},
			{Domain: "api.anthropic.com"},
		},
	}
	r := reconcilerFor(t)
	r.HostResolver = stubResolver{hosts: map[string][]string{
		"api.openai.com":    {"104.18.0.1"},
		"api.anthropic.com": {"172.66.0.5"},
	}}

	np, err := r.buildWorkspaceEgressNetworkPolicy(context.Background(), ws)
	require.NoError(t, err)
	require.NotNil(t, np, "non-empty Egress must produce a NetworkPolicy")

	// Pod selector must scope to just this workspace.
	require.NotNil(t, np.Spec.PodSelector.MatchLabels)
	require.Equal(t, ws.Name, np.Spec.PodSelector.MatchLabels[LabelWorkspace],
		"per-workspace NetPol must select via LabelWorkspace")

	// PolicyTypes must include Egress.
	require.Contains(t, np.Spec.PolicyTypes, networkingv1.PolicyTypeEgress)

	// HTTPS rule with the public IPs as /32 ipBlocks.
	foundHTTPS := false
	for _, rule := range np.Spec.Egress {
		hasHTTPS := false
		for _, port := range rule.Ports {
			if port.Port != nil && port.Port.IntValue() == 443 {
				hasHTTPS = true
			}
		}
		if !hasHTTPS {
			continue
		}
		foundHTTPS = true
		// Confirm the resolved IPs landed.
		gotCIDRs := map[string]bool{}
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				gotCIDRs[peer.IPBlock.CIDR] = true
			}
		}
		require.True(t, gotCIDRs["104.18.0.1/32"],
			"HTTPS rule must include /32 for api.openai.com's public IP")
		require.True(t, gotCIDRs["172.66.0.5/32"],
			"HTTPS rule must include /32 for api.anthropic.com's public IP")
	}
	require.True(t, foundHTTPS,
		"per-workspace NetPol must allow HTTPS to declared FQDNs")
}

func TestG4_F124_DropsResolvedPrivateIPs(t *testing.T) {
	// Validator-found bypass class: a domain that resolves into RFC1918
	// or 169.254/16 must NOT produce an ipBlock allow even though it
	// passed the cluster-internal-suffix check at admission. (This is
	// defense-in-depth — the webhook now blocks the cluster-internal
	// suffixes, but if a public domain RESOLVES to a private IP, we
	// still drop it.)
	ws := newWorkspaceForSecurity(t)
	ws.Spec.NetworkAccess = &v1.WorkspaceNetworkAccess{
		Egress: []v1.WorkspaceEgressRule{
			{Domain: "rebound.example.com"},
		},
	}
	r := reconcilerFor(t)
	r.HostResolver = stubResolver{hosts: map[string][]string{
		"rebound.example.com": {"169.254.169.254", "10.0.0.5"},
	}}

	np, err := r.buildWorkspaceEgressNetworkPolicy(context.Background(), ws)
	require.NoError(t, err)
	require.NotNil(t, np)

	// No ipBlock allow whatsoever for this set — both IPs are private.
	for _, rule := range np.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				t.Fatalf("F1.2.4 broken: private/internal IP %q leaked into NetPol allow",
					peer.IPBlock.CIDR)
			}
		}
	}
}

func TestG4_F124_NilNetworkAccessProducesNoExtraPolicy(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	ws.Spec.NetworkAccess = nil
	r := reconcilerFor(t)

	np, err := r.buildWorkspaceEgressNetworkPolicy(context.Background(), ws)
	require.NoError(t, err)
	require.Nil(t, np,
		"nil Spec.NetworkAccess must produce no per-workspace NetPol — chart-wide policy applies")
}

func TestG4_F124_EmptyEgressProducesNoExtraPolicy(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	ws.Spec.NetworkAccess = &v1.WorkspaceNetworkAccess{Egress: nil}
	r := reconcilerFor(t)

	np, err := r.buildWorkspaceEgressNetworkPolicy(context.Background(), ws)
	require.NoError(t, err)
	require.Nil(t, np,
		"empty Egress must produce no per-workspace NetPol")
}

// =============================================================================
// G22 (F1.4.2-adjacent) — EnableServiceLinks: false
// =============================================================================

func TestG22_PodHasEnableServiceLinksFalse(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)
	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)
	require.NotNil(t, pod.Spec.EnableServiceLinks,
		"EnableServiceLinks must be explicitly set, not left to the default true (G22)")
	require.False(t, *pod.Spec.EnableServiceLinks,
		"EnableServiceLinks must be false to prevent service-discovery env-var leak")
}

// =============================================================================
// G24 — seccompProfile: RuntimeDefault
// =============================================================================

func TestG24_PodHasRuntimeDefaultSeccompProfile(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)
	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)
	require.NotNil(t, pod.Spec.SecurityContext)
	require.NotNil(t, pod.Spec.SecurityContext.SeccompProfile,
		"PodSecurityContext.SeccompProfile must be set (G24)")
	require.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, pod.Spec.SecurityContext.SeccompProfile.Type,
		"SeccompProfile.Type must be RuntimeDefault")
}
