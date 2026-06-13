// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// imageTagFromPod reads from ContainerStatuses[0].ImageID (the resolved image
// after pull) and falls back to Spec.Containers[0].Image when ImageID is
// absent or carries no tag (e.g. containerd digest-only records).
//
// ImageID format varies by container runtime:
//
//	containerd digest-only : "ghcr.io/org/img@sha256:<hex>"         → no tag, fallback to spec
//	docker tag+digest      : "ghcr.io/org/img:ts-123@sha256:<hex>"  → extract "ts-123"
//	tag only               : "ghcr.io/org/img:ts-123"               → extract "ts-123"
//	bare digest            : "sha256:<hex>"                          → no tag, fallback to spec
//	empty ImageID          : ""                                      → fallback to spec
func TestImageTagFromPod_ContainerStatuses_TagAndDigest(t *testing.T) {
	// docker-style: tag + digest — should extract the tag
	pod := podWithImageID("ghcr.io/lenaxia/llmsafespace/base:ts-1781332002@sha256:32320b07abcd")
	got := imageTagFromPod(pod)
	if got != "ts-1781332002" {
		t.Errorf("tag+digest: want ts-1781332002, got %q", got)
	}
}

func TestImageTagFromPod_ContainerStatuses_DigestOnly_FallsBackToSpec(t *testing.T) {
	// containerd-style: digest only, no tag — must fall back to spec image
	pod := podWithImageIDAndSpecImage(
		"ghcr.io/lenaxia/llmsafespace/base@sha256:32320b07abcd",
		"ghcr.io/lenaxia/llmsafespace/base:ts-1781332002",
	)
	got := imageTagFromPod(pod)
	if got != "ts-1781332002" {
		t.Errorf("digest-only: want ts-1781332002 (from spec fallback), got %q", got)
	}
}

func TestImageTagFromPod_ContainerStatuses_TagOnly(t *testing.T) {
	// tag only in ImageID (local builds, some registries)
	pod := podWithImageID("ghcr.io/lenaxia/llmsafespace/base:ts-1781332002")
	got := imageTagFromPod(pod)
	if got != "ts-1781332002" {
		t.Errorf("tag-only ImageID: want ts-1781332002, got %q", got)
	}
}

func TestImageTagFromPod_ContainerStatuses_EmptyImageID_FallsBackToSpec(t *testing.T) {
	// ImageID empty (container not yet started) — must fall back to spec
	pod := podWithImageIDAndSpecImage(
		"",
		"ghcr.io/lenaxia/llmsafespace/base:ts-1781332002",
	)
	got := imageTagFromPod(pod)
	if got != "ts-1781332002" {
		t.Errorf("empty ImageID: want ts-1781332002 (from spec), got %q", got)
	}
}

func TestImageTagFromPod_ContainerStatuses_BareDigest_FallsBackToSpec(t *testing.T) {
	// sha256-only ImageID with no registry prefix or tag
	pod := podWithImageIDAndSpecImage(
		"sha256:32320b07abcdef1234567890",
		"ghcr.io/lenaxia/llmsafespace/base:ts-1781332002",
	)
	got := imageTagFromPod(pod)
	if got != "ts-1781332002" {
		t.Errorf("bare digest: want ts-1781332002 (from spec), got %q", got)
	}
}

func TestImageTagFromPod_NoContainerStatuses_FallsBackToSpec(t *testing.T) {
	// No ContainerStatuses at all — fall back to spec
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "ghcr.io/lenaxia/llmsafespace/base:ts-1781332002"},
			},
		},
	}
	got := imageTagFromPod(pod)
	if got != "ts-1781332002" {
		t.Errorf("no ContainerStatuses: want ts-1781332002, got %q", got)
	}
}

func TestImageTagFromPod_EmptyPod_ReturnsEmpty(t *testing.T) {
	// No spec containers, no statuses — return empty string
	got := imageTagFromPod(&corev1.Pod{})
	if got != "" {
		t.Errorf("empty pod: want empty string, got %q", got)
	}
}

func TestImageTagFromPod_NilPod_ReturnsEmpty(t *testing.T) {
	got := imageTagFromPod(nil)
	if got != "" {
		t.Errorf("nil pod: want empty string, got %q", got)
	}
}

func TestImageTagFromPod_SpecImageNoTag_ReturnsFullRef(t *testing.T) {
	// Spec image has no tag (unusual but valid) — return the full image ref
	pod := podWithImageIDAndSpecImage(
		"",
		"ghcr.io/lenaxia/llmsafespace/base",
	)
	got := imageTagFromPod(pod)
	if got != "ghcr.io/lenaxia/llmsafespace/base" {
		t.Errorf("spec no tag: want full ref, got %q", got)
	}
}

func TestImageTagFromPod_SpecImageWithLatestTag(t *testing.T) {
	pod := podWithImageIDAndSpecImage("", "ghcr.io/lenaxia/llmsafespace/base:latest")
	got := imageTagFromPod(pod)
	if got != "latest" {
		t.Errorf("latest tag: want latest, got %q", got)
	}
}

func TestImageTagFromPod_TagContainingHyphenAndNumbers(t *testing.T) {
	// Real-world ts- tags with unix timestamps
	pod := podWithImageID("ghcr.io/lenaxia/llmsafespace/base:ts-1781332002@sha256:aabbccdd")
	got := imageTagFromPod(pod)
	if got != "ts-1781332002" {
		t.Errorf("ts tag: want ts-1781332002, got %q", got)
	}
}

func TestImageTagFromPod_SHATagInImageID(t *testing.T) {
	// sha- prefixed commit tags
	pod := podWithImageID("ghcr.io/lenaxia/llmsafespace/base:sha-bf61310@sha256:32320b07abcd")
	got := imageTagFromPod(pod)
	if got != "sha-bf61310" {
		t.Errorf("sha tag: want sha-bf61310, got %q", got)
	}
}

func TestImageTagFromPod_RegistryWithPort_TagAndDigest(t *testing.T) {
	// Private registry with port number — colon in host must not be mistaken for tag separator.
	// registry.local:5000/org/img:ts-123@sha256:abc → tag is "ts-123", not "5000/org/img"
	pod := podWithImageID("registry.local:5000/org/img:ts-123@sha256:32320b07abcd")
	got := imageTagFromPod(pod)
	if got != "ts-123" {
		t.Errorf("registry with port tag+digest: want ts-123, got %q", got)
	}
}

func TestImageTagFromPod_RegistryWithPort_DigestOnly_FallsBackToSpec(t *testing.T) {
	// Private registry with port, digest-only ImageID — must fall back to spec.
	pod := podWithImageIDAndSpecImage(
		"registry.local:5000/org/img@sha256:32320b07abcd",
		"registry.local:5000/org/img:ts-123",
	)
	got := imageTagFromPod(pod)
	if got != "ts-123" {
		t.Errorf("registry with port digest-only: want ts-123 (spec fallback), got %q", got)
	}
}

func TestImageTagFromPod_SpecImage_RegistryWithPort(t *testing.T) {
	// Spec image with registry port — tagFromSpecImage must extract the tag, not the port.
	pod := podWithImageIDAndSpecImage("", "registry.local:5000/org/img:ts-123")
	got := imageTagFromPod(pod)
	if got != "ts-123" {
		t.Errorf("spec registry with port: want ts-123, got %q", got)
	}
}

// helpers

func podWithImageID(imageID string) *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "ghcr.io/lenaxia/llmsafespace/base:spec-tag"},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{ImageID: imageID},
			},
		},
	}
}

func podWithImageIDAndSpecImage(imageID, specImage string) *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: specImage},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{ImageID: imageID},
			},
		},
	}
}
