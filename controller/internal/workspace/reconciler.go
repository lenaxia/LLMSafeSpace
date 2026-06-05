package workspace

import (
	"context"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

type WorkspaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// HostResolver is used by the per-workspace NetworkPolicy generator
	// (network_policy.go) to resolve declared FQDNs to /32 ipBlocks at
	// reconcile time. Tests inject a stub; production uses
	// defaultHostResolver (net.DefaultResolver) when nil.
	HostResolver HostResolver

	// InferenceRelayURL is the Cloudflare Worker URL for free-tier inference
	// (Epic 26). When set, workspace pods route opencode provider requests
	// through this URL for IP distribution. When empty, opencode uses its
	// default gateway (opencode.ai/zen/v1) directly.
	InferenceRelayURL string

	// InferenceRelaySecret is the path-segment secret that authenticates
	// requests to the CF Worker. When set, it is appended to InferenceRelayURL
	// as the first path segment: https://relay.example.com/<secret>.
	// The Worker strips and validates this segment before forwarding upstream.
	// Set via --inference-relay-secret controller flag, sourced from a k8s Secret.
	InferenceRelaySecret string

	// lastDeepStatus tracks the last time enrichAgentStatus was called per
	// workspace. In-memory only — lost on controller restart (acceptable;
	// the next reconcile will just call it immediately).
	lastDeepStatus   map[string]time.Time
	lastDeepStatusMu sync.Mutex
}

func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", req.NamespacedName)

	workspace := &v1.Workspace{}
	if err := r.Get(ctx, req.NamespacedName, workspace); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !workspace.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, workspace)
	}

	switch workspace.Status.Phase {
	case "", v1.WorkspacePhasePending:
		return r.handlePending(ctx, workspace)
	case v1.WorkspacePhaseCreating:
		return r.handleCreating(ctx, workspace)
	case v1.WorkspacePhaseActive:
		return r.handleActive(ctx, workspace)
	case v1.WorkspacePhaseSuspending:
		return r.handleSuspending(ctx, workspace)
	case v1.WorkspacePhaseSuspended:
		return r.handleSuspended(ctx, workspace)
	case v1.WorkspacePhaseResuming:
		return r.handleResuming(ctx, workspace)
	case v1.WorkspacePhaseTerminating:
		return r.handleTerminating(ctx, workspace)
	case v1.WorkspacePhaseFailed:
		return r.handleFailed(ctx, workspace)
	default:
		logger.Info("Unknown workspace phase", "phase", workspace.Status.Phase)
		return ctrl.Result{}, nil
	}
}

func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Workspace{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

// sanitizeLabelValue maps a runtime image reference to a valid k8s
// label value. K8s label values must match
// `(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?` (max 63 chars, no
// `/`, `:`, `@`, etc.).
//
// Pre-fix this only replaced `:`. After image-pull-style runtimes
// became common (workspaces with `Spec.Runtime: ghcr.io/.../base:latest`
// — which the G2 webhook now requires), the slashes still in the
// value caused pod-creation kube-apiserver rejection:
//
//	metadata.labels: Invalid value: "ghcr.io/.../base_latest"
//
// We now also replace `/` and `@`, then truncate to 63 chars (k8s
// label-value max) preserving leading + trailing alphanumerics.
func sanitizeLabelValue(s string) string {
	r := strings.NewReplacer(":", "_", "/", "_", "@", "_")
	out := r.Replace(s)
	if len(out) > 63 {
		out = out[len(out)-63:]
	}
	for len(out) > 0 && !isLabelChar(out[0]) {
		out = out[1:]
	}
	for len(out) > 0 && !isLabelChar(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	if out == "" {
		out = "unspecified"
	}
	return out
}

func isLabelChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// imageTagFromPod extracts the image tag (portion after the last colon) from
// the first container's image reference. Returns the full image ref if no tag
// separator is found.
func imageTagFromPod(pod *corev1.Pod) string {
	if len(pod.Spec.Containers) == 0 {
		return ""
	}
	image := pod.Spec.Containers[0].Image
	if i := strings.LastIndex(image, ":"); i >= 0 {
		return image[i+1:]
	}
	return image
}
