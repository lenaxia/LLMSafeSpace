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

	"github.com/lenaxia/llmsafespace/pkg/agentd"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
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
	}

	annotations := map[string]string{
		"llmsafespace.dev/created-by": "controller",
	}
	if runtimeEnvName != "" {
		annotations["llmsafespace.dev/runtime-env"] = runtimeEnvName
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
			// F1.4.2 (Epic 17): Bearer token for agentd's /v1/readyz
			// and /v1/statusz endpoints. Sourced from the same per-
			// workspace password Secret the controller already
			// generates. The controller sends this token when polling
			// /v1/statusz; the kubelet's readiness probe must also
			// carry it via httpHeaders (set on the probe spec below).
			{Name: "AGENTD_ADMIN_TOKEN", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: passwordSecretName(workspace.Name),
					},
					Key: "password",
				},
			}},
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
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "sandbox-cfg", MountPath: "/sandbox-cfg", ReadOnly: true},
			{Name: "tmp", MountPath: "/tmp"},
			{Name: "sandbox-home", MountPath: "/home/sandbox"},
		},
		Resources: resourceRequirementsFor(workspace),
	}

	volumes := []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: workspace.Status.PVCName},
		}},
		// G15 (Epic 17): sandbox-cfg and tmp are tmpfs-backed to
		// prevent plaintext secrets / session keys from touching
		// node disk. sandbox-home is intentionally disk-backed so
		// tool caches (npm, pip, etc.) have sufficient space for
		// package downloads across sessions.
		{Name: "sandbox-cfg", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
			Medium:    corev1.StorageMediumMemory,
			SizeLimit: ptrQuantity("4Mi"),
		}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
			Medium:    corev1.StorageMediumMemory,
			SizeLimit: ptrQuantity("64Mi"),
		}}},
		{Name: "sandbox-home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
			SizeLimit: ptrQuantity("1Gi"),
		}}},
	}

	var initContainers []corev1.Container

	// Workspace setup init (packages + initScript).
	if len(workspace.Spec.Packages) > 0 || workspace.Spec.InitScript != "" {
		initContainers = append(initContainers, buildWorkspaceSetupInit(workspace, runtimeImage))
	}

	// Credential setup init.
	credInit, pwVolume, userSecretsVol, err := r.buildCredentialSetupInit(ctx, workspace, runtimeImage)
	if err != nil {
		return nil, err
	}
	initContainers = append(initContainers, credInit)
	volumes = append(volumes, pwVolume)
	volumes = append(volumes, *userSecretsVol)

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
//     500m CPU, 512Mi memory, 1Gi ephemeral-storage. This guarantees
//     every workspace carries at least basic limits even when the
//     operator submits a minimal YAML.
//   - Limits and Requests are set to the SAME value (1:1 mapping)
//     because the workspace is a single-tenant interactive
//     environment; QoS=Guaranteed simplifies eviction reasoning.
//   - Quantity parsing failures fall back to the default rather than
//     panicking. The CRD pattern + (future) webhook caps protect
//     against bad input; if both are bypassed (e.g. CRD validation
//     disabled cluster-wide), we degrade gracefully.
func resourceRequirementsFor(workspace *v1.Workspace) corev1.ResourceRequirements {
	const (
		defaultCPU       = "500m"
		defaultMemory    = "512Mi"
		defaultEphemeral = "1Gi"
		burstFactor      = 4
	)
	cpu := defaultCPU
	memory := defaultMemory
	ephemeral := defaultEphemeral
	cpuLimit := ""
	memoryLimit := ""
	if r := workspace.Spec.Resources; r != nil {
		if r.CPU != "" {
			cpu = r.CPU
		}
		if r.Memory != "" {
			memory = r.Memory
		}
		if r.EphemeralStorage != "" {
			ephemeral = r.EphemeralStorage
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
	ephReq := parseOrDefault(ephemeral, defaultEphemeral)

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
			corev1.ResourceCPU:              cpuReq,
			corev1.ResourceMemory:           memReq,
			corev1.ResourceEphemeralStorage: ephReq,
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              cpuLim,
			corev1.ResourceMemory:           memLim,
			corev1.ResourceEphemeralStorage: ephReq, // ephemeral: limit = request (no burst)
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

func (r *WorkspaceReconciler) buildCredentialSetupInit(ctx context.Context, workspace *v1.Workspace, runtimeImage string) (corev1.Container, corev1.Volume, *corev1.Volume, error) {
	credScript := `
if [ -f /mnt/secrets/user-secrets/secrets.json ]; then
  cp /mnt/secrets/user-secrets/secrets.json /sandbox-cfg/secrets.json
fi
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
	}

	// Always mount user-secrets with optional: true so the pod starts
	// cleanly even when no credentials have been configured yet. kubelet
	// will automatically sync the secret into the running pod within
	// ~60-90s once it is created, without requiring a pod restart.
	// The init script already guards with `if [ -f ... ]` so an empty
	// mount is safe.
	userSecretsName := fmt.Sprintf("workspace-secrets-%s", workspace.Name)
	optionalTrue := true
	userSecretsVolume := &corev1.Volume{
		Name: "user-secrets",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: userSecretsName,
				Optional:   &optionalTrue,
			},
		},
	}
	credMounts = append(credMounts, corev1.VolumeMount{
		Name: "user-secrets", MountPath: "/mnt/secrets/user-secrets", ReadOnly: true,
	})

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
		VolumeMounts: credMounts,
	}
	return credInit, pwVolume, userSecretsVolume, nil
}

func buildWorkspaceSetupInit(workspace *v1.Workspace, runtimeImage string) corev1.Container {
	trueVal := true
	falseVal := false
	return corev1.Container{
		Name:    "workspace-setup",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", buildWorkspaceSetupScript(workspace)},
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
