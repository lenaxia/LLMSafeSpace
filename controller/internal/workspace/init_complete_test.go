// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestAllInitContainersComplete_NoStatuses(t *testing.T) {
	pod := &corev1.Pod{}
	if allInitContainersComplete(pod) {
		t.Error("Should return false when no init container statuses")
	}
}

func TestAllInitContainersComplete_AllTerminatedSuccess(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
			},
		},
	}
	if !allInitContainersComplete(pod) {
		t.Error("Should return true when all init containers terminated with exit 0")
	}
}

func TestAllInitContainersComplete_OneStillRunning(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
				{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		},
	}
	if allInitContainersComplete(pod) {
		t.Error("Should return false when one init container is still running")
	}
}

func TestAllInitContainersComplete_OneFailedExitCode(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
			},
		},
	}
	if allInitContainersComplete(pod) {
		t.Error("Should return false when an init container failed (exit code != 0)")
	}
}

func TestAllInitContainersComplete_OneWaiting(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"}}},
			},
		},
	}
	if allInitContainersComplete(pod) {
		t.Error("Should return false when init container is waiting")
	}
}

func TestAllInitContainersComplete_SingleSuccess(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
			},
		},
	}
	if !allInitContainersComplete(pod) {
		t.Error("Should return true for single successful init container")
	}
}
