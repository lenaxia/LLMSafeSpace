// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestAllContainersReady_NilPod(t *testing.T) {
	if allContainersReady(nil) {
		t.Error("nil pod must return false")
	}
}

func TestAllContainersReady_NoStatuses(t *testing.T) {
	pod := &corev1.Pod{}
	if allContainersReady(pod) {
		t.Error("pod with no ContainerStatuses must return false")
	}
}

func TestAllContainersReady_AllReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true},
				{Ready: true},
			},
		},
	}
	if !allContainersReady(pod) {
		t.Error("all-ready containers must return true")
	}
}

func TestAllContainersReady_OneNotReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true},
				{Ready: false},
			},
		},
	}
	if allContainersReady(pod) {
		t.Error("one not-ready container must return false")
	}
}

func TestAllContainersReady_AllNotReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: false},
				{Ready: false},
			},
		},
	}
	if allContainersReady(pod) {
		t.Error("all not-ready containers must return false")
	}
}

func TestAllContainersReady_SingleReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true},
			},
		},
	}
	if !allContainersReady(pod) {
		t.Error("single ready container must return true")
	}
}
