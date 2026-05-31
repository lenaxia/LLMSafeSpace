// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import corev1 "k8s.io/api/core/v1"

// isPodTerminating reports whether the K8s pod is in the process of being
// deleted. A non-nil DeletionTimestamp means kubelet is running termination
// grace; the pod's Status.Phase during this window is unreliable for
// failure-classification purposes (e.g., a SIGKILLed container makes the
// pod briefly observable as Failed). Callers should treat such pods as
// "wait for reaping" rather than as genuine failures.
//
// US-23.1: This check prevents the worklog 0100 incident class where the
// controller deletes a pod via checkAgentHealth, then handleCreating
// observes the dying pod in K8s phase=Failed and writes terminal Failed.
func isPodTerminating(pod *corev1.Pod) bool {
	return pod != nil && pod.DeletionTimestamp != nil
}
