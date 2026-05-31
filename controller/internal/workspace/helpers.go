// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// isPodTerminating reports whether the K8s pod is in the process of being
// deleted. A non-nil DeletionTimestamp means kubelet is running termination
// grace; the pod's Status.Phase during this window is unreliable for
// failure-classification purposes (e.g., a SIGKILLed container makes the
// pod briefly observable as Failed). Callers should treat such pods as
// "wait for reaping" rather than as genuine failures.
func isPodTerminating(pod *corev1.Pod) bool {
	return pod != nil && pod.DeletionTimestamp != nil
}

// markFailed transitions a workspace to Failed with a typed FailureReason
// and a human-readable message. All Failed-write sites must use this helper
// to ensure FailureReason is always populated.
func markFailed(ws *v1.Workspace, reason v1.FailureReason, format string, args ...any) {
	ws.Status.Phase = v1.WorkspacePhaseFailed
	ws.Status.FailureReason = reason
	ws.Status.Message = fmt.Sprintf(format, args...)
}
