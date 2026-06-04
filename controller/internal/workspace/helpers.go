// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	corev1 "k8s.io/api/core/v1"
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

// allContainersReady reports whether every container in the pod has passed its
// readiness probe. Kubernetes sets ContainerStatus.Ready=true only after the
// readiness probe succeeds, so this is the correct gate for "application is
// ready to serve traffic" — distinct from PodPhase==Running, which only means
// the container process has started.
//
// Returns false for a nil pod or a pod with no ContainerStatuses (e.g. the
// status subresource has not yet been populated by kubelet).
func allContainersReady(pod *corev1.Pod) bool {
	if pod == nil || len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}
