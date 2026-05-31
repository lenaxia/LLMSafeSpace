// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// +kubebuilder:webhook:path=/validate-llmsafespace-dev-v1-workspace,mutating=false,failurePolicy=fail,groups=llmsafespace.dev,resources=workspaces,verbs=create;update,versions=v1,name=vworkspace.kb.io,sideEffects=None,admissionReviewVersions=v1

// WorkspaceValidator is a ValidatingAdmissionWebhook for Workspace resources.
//
// It closes the following pentest findings (Epic 17):
//   - F1.2.1 / RT-2.18 / RT-6.10 (Critical): Spec.Runtime arbitrary image
//     pull. Without an allow-list, a user could create a Workspace with
//     `runtime: "evil.example.com/malicious:latest"` and the controller
//     would pull and run that image.
//   - F1.2.2 (Critical): Status forge. On CREATE the kube-apiserver does
//     NOT yet apply status-subresource semantics, so a malicious user
//     can stamp `status.podIP` / `status.podName` / `status.endpoint`
//     and the API proxy will route requests to the attacker-supplied
//     pod-IP. Defense in depth: also reject status mutations on UPDATE
//     through the spec endpoint (the kube-apiserver subresource split
//     normally enforces this, but failure-modes during CRD upgrades
//     have surfaced it as a real risk).
//   - F1.2.9 (Medium): Spec.Storage.StorageClassName had no allow-list,
//     letting users target hostPath / NFS / arbitrary CSIs.
//   - RT-6.1 (High): Webhook accepted `runtime: "../../etc/passwd"` and
//     `storage.size: "999999Gi"` (CRD pattern allowed any digit count).
//
// The validator is configurable so the same chart works for every
// deployment topology — operators decide which registries and storage
// classes are safe in their environment.
//
// Field reference (set by the controller manager at construction):
//   - Decoder: required; nil decoder makes Handle deny with a clear
//     error rather than panic on nil-pointer-deref.
//   - AllowedImageRegistries: list of registry prefixes (e.g.
//     "ghcr.io/lenaxia/", "registry.k8s.io/"). A Workspace whose Runtime
//     contains "/" (i.e. is shaped like an explicit image reference)
//     must match at least one prefix.
//   - AllowedStorageClassNames: optional. If non-nil, the Spec.Storage.
//     StorageClassName must be in this list (empty StorageClassName
//     always passes — that means "use cluster default").
//   - MaxStorageGi: maximum requested workspace storage in GiB. Any
//     storage size above this is rejected. Set 0 to disable.
type WorkspaceValidator struct {
	Decoder                  admission.Decoder
	AllowedImageRegistries   []string
	AllowedStorageClassNames []string
	MaxStorageGi             int64
}

// runtimeRefIsImage reports whether the runtime string looks like an
// explicit container image reference. The convention used by
// `runtime_resolver.go` is: presence of '/' triggers image-pull rather
// than RuntimeEnvironment lookup.
func runtimeRefIsImage(s string) bool {
	return strings.Contains(s, "/")
}

// runtimeRunSafePattern accepts:
//   - DNS-style names with optional ':tag' or '@digest' suffix.
//   - Lowercase letters, digits, dots, slashes, dashes, underscores,
//     colon, and '@'.
//
// Anything else is a NAK.
var runtimeRunSafePattern = regexp.MustCompile(`^[a-zA-Z0-9._/:@-]+$`)

// runtimeRefHasTraversal flags path-traversal / NUL / backslash payloads.
// These never appear in legitimate image references.
func runtimeRefHasTraversal(s string) bool {
	if strings.Contains(s, "..") {
		return true
	}
	if strings.ContainsAny(s, "\x00\\ \t\n\r") {
		return true
	}
	return false
}

// storageSizePattern is a stricter form of the CRD pattern. The CRD
// allows any number of digits; we additionally enforce an upper bound
// in storageSizeGi.
var storageSizePattern = regexp.MustCompile(`^([0-9]+)(Gi|Mi)$`)

// storageSizeGi parses the spec.storage.size string and returns the
// value in GiB, rounding Mi up to 1Gi (so 256Mi reports as 1Gi). Returns
// (-1, error) on malformed input.
func storageSizeGi(s string) (int64, error) {
	m := storageSizePattern.FindStringSubmatch(s)
	if m == nil {
		return -1, fmt.Errorf("storage.size %q does not match ^[0-9]+(Gi|Mi)$", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n < 1 {
		return -1, fmt.Errorf("storage.size %q has invalid magnitude", s)
	}
	if m[2] == "Mi" {
		// Round up: any Mi value occupies at most 1Gi from the quota's
		// perspective. Avoids spurious "0Gi" results for small allocations.
		gi := (n + 1023) / 1024
		if gi < 1 {
			gi = 1
		}
		return gi, nil
	}
	return n, nil
}

// statusIsZero reports whether the Status block contains only zero
// values. A user creating a Workspace must not set any Status field;
// only the controller writes Status (via the status subresource).
func statusIsZero(s v1.WorkspaceStatus) bool {
	return reflect.DeepEqual(s, v1.WorkspaceStatus{})
}

// Handle validates the Workspace resource. Errors are returned as
// admission.Denied with a human-readable message rather than as 5xx
// admission errors so kubectl shows the operator the precise reason.
func (v *WorkspaceValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if v.Decoder == nil {
		return admission.Errored(http.StatusInternalServerError,
			fmt.Errorf("workspace webhook: decoder is not configured"))
	}

	ws := &v1.Workspace{}
	if err := v.Decoder.Decode(req, ws); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// 1. Runtime is required.
	if strings.TrimSpace(ws.Spec.Runtime) == "" {
		return admission.Denied("spec.runtime is required")
	}

	// 1a. Length cap — RE2 is linear so no ReDoS, but unbounded strings
	//     are still wasteful and unrealistic. Container image references
	//     never exceed 255 chars (registry+repo+tag/digest); a 512-char
	//     ceiling is generous.
	if len(ws.Spec.Runtime) > 512 {
		return admission.Denied(
			"spec.runtime exceeds the 512-character length limit")
	}
	// Same for storage class name. Kubernetes object names cap at 253.
	if len(ws.Spec.Storage.StorageClassName) > 253 {
		return admission.Denied(
			"spec.storage.storageClassName exceeds the 253-character limit")
	}

	// 2. Runtime must not contain path-traversal / NUL / whitespace.
	if runtimeRefHasTraversal(ws.Spec.Runtime) {
		return admission.Denied(
			"spec.runtime contains forbidden characters (path-traversal, whitespace, NUL or backslash)")
	}
	if !runtimeRunSafePattern.MatchString(ws.Spec.Runtime) {
		return admission.Denied(
			"spec.runtime contains characters outside the allowed set [a-zA-Z0-9._/:@-]")
	}

	// 3. If runtime references an explicit image (contains '/'), it MUST
	//    match a configured registry allow-list prefix. Reject otherwise.
	//    A reference without '/' (e.g. "python-3.11") is a
	//    RuntimeEnvironment name lookup; the controller validates the
	//    target exists at reconcile time.
	//
	//    Allow-list prefix safety: each prefix MUST end with '/' so
	//    `HasPrefix("ghcr.io/lenaxia.attacker.com/...", "ghcr.io/lenaxia/")`
	//    cannot accidentally match. We enforce that here rather than
	//    trust the operator to remember the trailing slash. Prefixes
	//    without a trailing '/' are silently treated as `prefix + "/"`.
	if runtimeRefIsImage(ws.Spec.Runtime) {
		matched := false
		for _, prefix := range v.AllowedImageRegistries {
			if prefix == "" {
				continue
			}
			normalized := prefix
			if !strings.HasSuffix(normalized, "/") {
				normalized += "/"
			}
			if strings.HasPrefix(ws.Spec.Runtime, normalized) {
				matched = true
				break
			}
		}
		if !matched {
			allowed := strings.Join(v.AllowedImageRegistries, ", ")
			if allowed == "" {
				allowed = "(none — operator must populate webhooks.allowedImageRegistries)"
			}
			return admission.Denied(fmt.Sprintf(
				"spec.runtime %q is an explicit image reference but its registry is not in the allow-list. Allowed registry prefixes: %s",
				ws.Spec.Runtime, allowed))
		}
	}

	// 4. Storage size: enforce the CRD pattern AND an upper bound.
	if strings.TrimSpace(ws.Spec.Storage.Size) == "" {
		return admission.Denied("spec.storage.size is required")
	}
	gi, err := storageSizeGi(ws.Spec.Storage.Size)
	if err != nil {
		return admission.Denied(err.Error())
	}
	if v.MaxStorageGi > 0 && gi > v.MaxStorageGi {
		return admission.Denied(fmt.Sprintf(
			"spec.storage.size %s (%d Gi) exceeds the maximum %d Gi configured for this cluster",
			ws.Spec.Storage.Size, gi, v.MaxStorageGi))
	}

	// 5. StorageClassName allow-list (optional). Empty = use cluster
	//    default and is always permitted.
	if v.AllowedStorageClassNames != nil && ws.Spec.Storage.StorageClassName != "" {
		matched := false
		for _, sc := range v.AllowedStorageClassNames {
			if sc == ws.Spec.Storage.StorageClassName {
				matched = true
				break
			}
		}
		if !matched {
			return admission.Denied(fmt.Sprintf(
				"spec.storage.storageClassName %q is not in the allow-list %v",
				ws.Spec.Storage.StorageClassName, v.AllowedStorageClassNames))
		}
	}

	// 6. F1.2.2 — Status must not be set by the user. On CREATE only the
	//    controller (via status subresource) is allowed to populate the
	//    block.
	if req.Operation == "CREATE" && !statusIsZero(ws.Status) {
		return admission.Denied(
			"spec.status fields must not be set on CREATE; the controller writes status via the status subresource")
	}

	// 7. F1.2.2 (defense in depth) — On UPDATE, refuse to mutate Status
	//    via the spec endpoint. The kube-apiserver normally enforces
	//    this via the subresource split (writes to /workspaces ignore
	//    Status), but a CRD-upgrade race or a misconfigured aggregator
	//    could lift that enforcement; we re-check at admission.
	//
	//    Validator-bypass note (worklog 0096): an empty
	//    `req.OldObject.Raw` was previously silently allowing through
	//    UPDATEs because the comparison had nothing to compare against.
	//    We now treat that case as "old status was zero" — i.e. any
	//    non-zero status on the new object is rejected. AdmissionReview
	//    v1 does not strictly require OldObject on UPDATE, so we fail
	//    closed when it is missing.
	if req.Operation == "UPDATE" {
		var oldStatus v1.WorkspaceStatus
		if len(req.OldObject.Raw) > 0 {
			old := &v1.Workspace{}
			if err := v.Decoder.DecodeRaw(req.OldObject, old); err != nil {
				return admission.Errored(http.StatusBadRequest,
					fmt.Errorf("decoding old workspace for status comparison: %w", err))
			}
			oldStatus = old.Status
		}
		if !reflect.DeepEqual(ws.Status, oldStatus) {
			return admission.Denied(
				"spec.status mutations through the workspaces endpoint are not allowed; use the workspaces/status subresource")
		}
	}

	return admission.Allowed("workspace is valid")
}

// InjectDecoder retained for backwards compatibility (see SandboxValidator).
func (v *WorkspaceValidator) InjectDecoder(d admission.Decoder) error {
	v.Decoder = d
	return nil
}
