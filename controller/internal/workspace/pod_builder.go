package workspace

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

func (r *WorkspaceReconciler) buildPod(ctx context.Context, workspace *v1.Workspace) (*corev1.Pod, error) {
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	runtimeImage, runtimeEnvName, err := resolveRuntimeImage(ctx, r.Client, workspace.Spec.Runtime)
	if err != nil {
		return nil, fmt.Errorf("resolving runtime image: %w", err)
	}

	// F1.4.2 (Epic 17): Read the per-workspace admin token from the
	// password Secret. Used as the `Authorization: Bearer <token>`
	// header for the readiness probe so kubelet can hit the
	// authenticated /v1/readyz endpoint. ensurePasswordSecret() runs
	// in handlePending before buildPod is reached, so the Secret
	// is guaranteed to exist; if Get fails we fall back to omitting
	// the header (probe will fail closed and the pod won't be ready
	// — observable + safe).
	adminToken := ""
	pwSec := &corev1.Secret{}
	if pwErr := r.Get(ctx, types.NamespacedName{Name: passwordSecretName(workspace.Name), Namespace: workspace.Namespace}, pwSec); pwErr == nil {
		if v, ok := pwSec.Data["password"]; ok {
			adminToken = string(v)
		}
	}

	labels := map[string]string{
		LabelApp:       AppName,
		LabelComponent: ComponentWorkspace,
		LabelWorkspace: workspace.Name,
		LabelRuntime:   sanitizeLabelValue(workspace.Spec.Runtime),
		LabelTenant:    sanitizeLabelValue(tenantID(workspace.Spec.Owner)),
	}

	annotations := map[string]string{
		"llmsafespaces.dev/created-by": "controller",
	}
	if runtimeEnvName != "" {
		annotations["llmsafespaces.dev/runtime-env"] = runtimeEnvName
	}

	trueVal := true
	falseVal := false

	mainContainer := corev1.Container{
		Name:    "workspace",
		Image:   runtimeImage,
		Command: []string{"/usr/local/bin/entrypoint-opencode.sh"},
		Ports: []corev1.ContainerPort{
			{ContainerPort: agentd.AgentPort, Name: "opencode", Protocol: corev1.ProtocolTCP},
			{ContainerPort: agentd.AgentdPort, Name: "agentd", Protocol: corev1.ProtocolTCP},
			{ContainerPort: agentd.AgentdAdminPort, Name: "agentd-admin", Protocol: corev1.ProtocolTCP},
		},
		Env: []corev1.EnvVar{
			{Name: "WORKSPACE_ID", Value: workspace.Name},
			{Name: "WORKSPACE_DIR", Value: agentd.WorkspacePath},
			{Name: "AGENTD_ADMIN_TOKEN", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: passwordSecretName(workspace.Name),
					},
					Key: "password",
				},
			}},
			// Enable the opencode v2 event system so session.next.step.ended
			// is emitted to the /event SSE stream. Without this flag the API
			// proxy never receives token-usage events and session_index.context_used
			// stays NULL for every session, causing the Sidebar to show "0/Unknown".
			// Proven by live cluster experiment: setting this flag on a running pod
			// caused context_used to be written within one second of the next LLM
			// step completing. See worklog 0263.
			{Name: "OPENCODE_EXPERIMENTAL_EVENT_SYSTEM", Value: "true"},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/v1/readyz",
					Port: intstr.FromInt(agentd.AgentdAdminPort),
					HTTPHeaders: func() []corev1.HTTPHeader {
						if adminToken == "" {
							return nil
						}
						return []corev1.HTTPHeader{
							{Name: "Authorization", Value: "Bearer " + adminToken},
						}
					}(),
				},
			},
			InitialDelaySeconds: 10, PeriodSeconds: 15, TimeoutSeconds: 3, FailureThreshold: 5,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/v1/healthz",
					Port: intstr.FromInt(agentd.AgentdAdminPort),
				},
			},
			InitialDelaySeconds: 15, PeriodSeconds: 30, TimeoutSeconds: 5, FailureThreshold: 6,
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: []corev1.VolumeMount{
			// The workspace PVC contains three named subtrees via explicit subPaths:
			//   workspace/ — user workspace data, opencode.db, auth.json
			//   home/      — SSH keys, secrets base dir, enricher cache, tool caches
			//   tmp/       — agent-config.json, secrets-env; agentd rewrites these
			//                on each credential cycle. Other files persist across
			//                pod restarts (PVC-backed, not ephemeral).
			// workspace-dirs init container unconditionally creates all three
			// subdirectories at the PVC root before any subPath mount is attempted.
			{Name: "workspace", MountPath: "/workspace", SubPath: "workspace"},
			{Name: "sandbox-cfg", MountPath: "/sandbox-cfg", ReadOnly: true},
			{Name: "workspace", MountPath: "/tmp", SubPath: "tmp"},
			{Name: "workspace", MountPath: "/home/sandbox", SubPath: "home"},
		},
		Resources: resourceRequirementsFor(workspace),
	}

	volumes := []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: workspace.Status.PVCName},
		}},
		// G15 (Epic 17): sandbox-cfg is tmpfs-backed (Memory medium) to
		// prevent plaintext secrets / session keys from touching node disk.
		// /tmp is now a subPath on the workspace PVC (see SubPath: "tmp" mount
		// above) so agent-config.json and secrets-env survive pod restarts and
		// are subject to the same Longhorn redundancy as other workspace data.
		// The agentd Materializer.reset() deletes agent-config.json and
		// secrets-env at the start of each credential materialize cycle, so
		// these specific files are always freshly written. Other files written
		// to /tmp by packages or agent processes persist across pod restarts.
		{Name: "sandbox-cfg", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
			Medium:    corev1.StorageMediumMemory,
			SizeLimit: ptrQuantity("4Mi"),
		}}},
	}

	var initContainers []corev1.Container

	// workspace-dirs init: unconditionally ensures all three PVC subPath
	// directories exist at the PVC root before any other init or the main
	// container mounts them. Without this, kubelet fails the pod with
	// "subPath not found" on a fresh PVC. Runs as the same non-root UID as
	// the main container; writes only to the PVC root.
	initContainers = append(initContainers, buildWorkspaceDirsInit(runtimeImage))

	// Epic 26: inject relay baseURL so agentd can configure the opencode provider
	// to route free-tier inference through the Cloudflare Worker for IP distribution.
	// When InferenceRelaySecret is set it is embedded as the first path segment;
	// the Worker strips and validates it before forwarding to upstream.
	if r.InferenceRelayURL != "" {
		relayBaseURL := r.InferenceRelayURL
		if r.InferenceRelaySecret != "" {
			relayBaseURL = r.InferenceRelayURL + "/" + r.InferenceRelaySecret
		}
		mainContainer.Env = append(mainContainer.Env,
			corev1.EnvVar{Name: "INFERENCE_RELAY_BASEURL", Value: relayBaseURL},
		)
	}

	// Workspace setup init (packages + initScript).
	if len(workspace.Spec.Packages) > 0 || workspace.Spec.InitScript != "" {
		initContainers = append(initContainers, buildWorkspaceSetupInit(workspace, runtimeImage))
	}

	// Credential setup init.
	credInit, pwVolume, bootstrapTokenVol, err := r.buildCredentialSetupInit(workspace, runtimeImage)
	if err != nil {
		return nil, err
	}
	initContainers = append(initContainers, credInit)
	volumes = append(volumes, pwVolume)
	volumes = append(volumes, bootstrapTokenVol)

	// Epic 51 S51.1: Runtime class resolution. Per-workspace opt-out
	// (spec.runtimeClass) takes precedence; otherwise use the controller's
	// DefaultRuntimeClass (typically "gvisor" in production multi-tenant).
	// Empty string = runc (K8s default).
	runtimeClassName := r.DefaultRuntimeClass
	if workspace.Spec.RuntimeClass != nil {
		runtimeClassName = *workspace.Spec.RuntimeClass
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   workspace.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			InitContainers: initContainers,
			Containers:     []corev1.Container{mainContainer},
			Volumes:        volumes,
			NodeSelector:   buildNodeSelector(workspace),
			// G17 (Epic 17): Sandbox pods MUST NOT automount the default
			// ServiceAccount token. The agent has no business calling the
			// K8s API; mounting the token only widens the blast radius for
			// a compromised sandbox. Without this, kubelet writes a JWT to
			// /var/run/secrets/kubernetes.io/serviceaccount/token that any
			// process inside the pod can read. See
			// `controller/internal/workspace/security_test.go` for the
			// regression that locks this in.
			//
			// Epic 35: the pod runs under the per-workspace SA
			// (workspace-<name>) so the projected SA token volume in the
			// init container is for the correct identity. AutomountServiceAccountToken
			// stays false — the projected token is an explicit volume mount
			// (init container only), not the default automount (which would
			// also appear in the main container).
			ServiceAccountName:           bootstrapSAName(workspace.Name),
			AutomountServiceAccountToken: &falseVal,
			// G22 (Epic 17 worklog 0088 RT-3.3): EnableServiceLinks
			// defaults to true in K8s, which materializes 30+
			// `<SVC>_SERVICE_HOST/PORT` env vars in the workspace
			// pod's PID-1 environ. This leaks namespace topology to
			// any process inside the sandbox (and to anyone who can
			// read /proc/PID/environ). Disable explicitly.
			EnableServiceLinks: &falseVal,
			SecurityContext:    buildPodSecurityContext(workspace),
		},
	}
	if runtimeClassName != "" {
		pod.Spec.RuntimeClassName = &runtimeClassName
	}
	return pod, nil
}

// resourceRequirementsFor maps the Workspace's spec.resources to a
// corev1.ResourceRequirements block. Closes F1.2.3 (Epic 17): pre-fix
// the controller never applied the operator-supplied resource limits,
// so workspace pods ran without quota and could DoS the node.
//
// Behavior:
//   - If spec.resources is nil, fall back to a sane default (matches
//     the kubebuilder defaults documented on `WorkspaceSpec`):
//     500m CPU, 512Mi memory. This guarantees every workspace carries
//     at least basic limits even when the operator submits a minimal
//     YAML.
//   - ephemeral-storage is intentionally NOT set on the pod. The
//     workspace's writable surfaces (PVC subPaths for /workspace,
//     /home, /tmp; Memory-backed emptyDir for /sandbox-cfg) do not
//     count toward node ephemeral storage. The only consumer is
//     kubelet's container log files, which kubelet already rotates
//     (~50 MiB per pod). A per-pod ephemeral limit added no
//     protection beyond what kubelet's own log rotation provides.
//   - Quantity parsing failures fall back to the default rather than
//     panicking. The CRD pattern + (future) webhook caps protect
//     against bad input; if both are bypassed (e.g. CRD validation
//     disabled cluster-wide), we degrade gracefully.
func resourceRequirementsFor(workspace *v1.Workspace) corev1.ResourceRequirements {
	const (
		defaultCPU    = "500m"
		defaultMemory = "512Mi"
		burstFactor   = 4
	)
	cpu := defaultCPU
	memory := defaultMemory
	cpuLimit := ""
	memoryLimit := ""
	if r := workspace.Spec.Resources; r != nil {
		if r.CPU != "" {
			cpu = r.CPU
		}
		if r.Memory != "" {
			memory = r.Memory
		}
		cpuLimit = r.CPULimit
		memoryLimit = r.MemoryLimit
	}
	parseOrDefault := func(s, fallback string) resource.Quantity {
		if q, err := resource.ParseQuantity(s); err == nil {
			return q
		}
		return resource.MustParse(fallback)
	}

	cpuReq := parseOrDefault(cpu, defaultCPU)
	memReq := parseOrDefault(memory, defaultMemory)

	// CPU limit: explicit > 4× request
	var cpuLim resource.Quantity
	if cpuLimit != "" {
		if q, err := resource.ParseQuantity(cpuLimit); err == nil {
			cpuLim = q
		} else {
			cpuLim = multiplyQuantity(cpuReq, burstFactor)
		}
	} else {
		cpuLim = multiplyQuantity(cpuReq, burstFactor)
	}

	// Memory limit: explicit > 4× request
	var memLim resource.Quantity
	if memoryLimit != "" {
		if q, err := resource.ParseQuantity(memoryLimit); err == nil {
			memLim = q
		} else {
			memLim = multiplyQuantity(memReq, burstFactor)
		}
	} else {
		memLim = multiplyQuantity(memReq, burstFactor)
	}

	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    cpuReq,
			corev1.ResourceMemory: memReq,
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    cpuLim,
			corev1.ResourceMemory: memLim,
		},
	}
}

func multiplyQuantity(q resource.Quantity, factor int64) resource.Quantity {
	if q.Format == resource.DecimalSI {
		return *resource.NewMilliQuantity(q.MilliValue()*factor, resource.DecimalSI)
	}
	return *resource.NewQuantity(q.Value()*factor, q.Format)
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
		// G24 (Epic 17 worklog 0088 RT-3.7): RuntimeDefault seccomp
		// profile blocks dangerous syscalls (unshare/clone/keyctl/
		// ptrace/etc.) at the kernel level. Defense-in-depth — cap-
		// drop ALL + NoNewPrivs:1 already EPERM these, but
		// RuntimeDefault hardens the boundary further at zero cost.
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

func buildNodeSelector(workspace *v1.Workspace) map[string]string {
	arch := workspace.Spec.Architecture
	if arch == "" {
		arch = "amd64"
	}
	return map[string]string{
		"kubernetes.io/arch": arch,
	}
}

func (r *WorkspaceReconciler) buildCredentialSetupInit(workspace *v1.Workspace, runtimeImage string) (corev1.Container, corev1.Volume, corev1.Volume, error) {
	credScript := `
workspace-agentd bootstrap --workspace-id "$WORKSPACE_ID" --api-url "$LLMSAFESPACE_API_URL"
workspace-agentd materialize
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
		{Name: "bootstrap-token", MountPath: "/var/run/bootstrap", ReadOnly: true},
	}

	// Epic 35 US-35.4: projected SA token volume. The kubelet creates a token
	// for the pod's ServiceAccount (workspace-<name>) with the specified
	// audience and expiry. Mounted only on the init container — the main
	// container never sees this token (AutomountServiceAccountToken: false
	// suppresses the default mount; this is an explicit projected volume).
	tokenTTL := int64(300)
	bootstrapTokenVolume := corev1.Volume{
		Name: "bootstrap-token",
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{{
					ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
						Path:              "token",
						ExpirationSeconds: &tokenTTL,
						Audience:          bootstrapAudience,
					},
				}},
			},
		},
	}

	trueVal := true
	falseVal := false
	credInit := corev1.Container{
		Name:    "credential-setup",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", credScript},
		Env: []corev1.EnvVar{
			{Name: "WORKSPACE_ID", Value: workspace.Name},
			{Name: "LLMSAFESPACE_API_URL", Value: r.APIServiceURL},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: credMounts,
	}
	return credInit, pwVolume, bootstrapTokenVolume, nil
}

// buildWorkspaceDirsInit returns an always-running init container that creates
// the three PVC subPath directories (workspace/, home/, tmp/) at the PVC root
// before any other init or the main container attempts to mount them.
// Without this, kubelet fails the pod with "subPath not found" on a fresh PVC.
func buildWorkspaceDirsInit(runtimeImage string) corev1.Container {
	trueVal := true
	falseVal := false
	return corev1.Container{
		Name:    "workspace-dirs",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", "mkdir -p /pvc/workspace /pvc/home /pvc/tmp"},
		VolumeMounts: []corev1.VolumeMount{
			// Mount PVC root (no subPath) so we can create the subdirectories.
			{Name: "workspace", MountPath: "/pvc"},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
}

func buildWorkspaceSetupInit(workspace *v1.Workspace, runtimeImage string) corev1.Container {
	trueVal := true
	falseVal := false
	return corev1.Container{
		Name:    "workspace-setup",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", buildWorkspaceSetupScript(workspace)},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace", SubPath: "workspace"},
			{Name: "workspace", MountPath: "/tmp", SubPath: "tmp"},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
}

// shellQuoteSingle wraps an argument in POSIX single quotes, escaping
// any embedded single-quote bytes via the standard `'\”` pattern.
// The result is safe to pass to /bin/sh as a single positional
// argument: nothing inside the quotes is interpreted by the shell.
//
// Closes F1.2.5 (Epic 17): pre-fix the controller did
//
//	args += " " + req
//	script += "pip install --target=... " + args
//
// which let an adversarial requirement string contain shell
// metacharacters (`;`, `|`, `\“, `$()`) and break out of the pip
// invocation. Post-fix every requirement is wrapped in single quotes,
// so the only thing pip / npm / go install ever sees is the literal
// requirement bytes — which they will reject as a parse error if
// adversarial. Defense in depth: the admission webhook also rejects
// these payloads at CREATE/UPDATE.
func shellQuoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func buildWorkspaceSetupScript(ws *v1.Workspace) string {
	script := "#!/bin/sh\nset -e\nmkdir -p /workspace/packages\n"
	for _, pkgSet := range ws.Spec.Packages {
		if len(pkgSet.Requirements) == 0 {
			continue
		}
		args := ""
		for _, req := range pkgSet.Requirements {
			args += " " + shellQuoteSingle(req)
		}
		rt := pkgSet.Runtime
		// `--` after the package-manager flags terminates argv parsing,
		// so even if a requirement somehow starts with `-` (admission
		// is normally blocking that), the package manager will treat
		// it as a positional argument and reject it as an unknown
		// package name rather than parsing it as a flag (RCE class —
		// see worklog 0098 / F1.2.5 validator pass 2).
		switch {
		case len(rt) >= 6 && rt[:6] == "nodejs":
			script += "cd /workspace/packages && npm install --" + args + "\n"
		case len(rt) >= 2 && rt[:2] == "go":
			for _, req := range pkgSet.Requirements {
				// `go install` does not support `--`; we rely on the
				// admission webhook + shellQuoteSingle. The webhook
				// rejects leading `-` and URL-shaped strings.
				script += "cd /workspace/packages && go install " + shellQuoteSingle(req) + "\n"
			}
		default:
			script += "pip install --target=/workspace/packages --" + args + "\n"
		}
	}
	if ws.Spec.InitScript != "" {
		// InitScript is ALREADY a multi-line shell payload deliberately
		// authored by the workspace owner. We do NOT shell-quote it (it
		// is meant to BE a script). The here-document delimiter
		// `INITSCRIPT` is literal-quoted so embedded $variables and
		// $(commands) are preserved verbatim. F1.2.5 explicitly does
		// NOT cover InitScript — that is by design a code-execution
		// surface.
		script += "cat > /tmp/init-script.sh << 'INITSCRIPT'\n"
		script += ws.Spec.InitScript + "\n"
		script += "INITSCRIPT\n"
		script += "chmod +x /tmp/init-script.sh\n"
		script += "/tmp/init-script.sh\n"
	}
	return script
}

// --- Setup ---
