// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

// Tests for relay baseURL injection into workspace pods (Epic 26).
// The controller injects INFERENCE_RELAY_BASEURL as an env var on the
// main container when InferenceRelayURL is configured. agentd reads this
// at startup and calls opencode's PUT /auth/opencode to set the provider
// baseURL, routing free-tier inference through the CF Worker.
// The CF Worker URL itself acts as the access credential (no separate secret).

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/pkg/agent/opencode"
)

func init() { opencode.Register() }

// TestBuildPod_RelayBaseURL_NotInjectedWhenEmpty verifies that when
// InferenceRelayURL is empty, INFERENCE_RELAY_BASEURL is not added.
func TestBuildPod_RelayBaseURL_NotInjectedWhenEmpty(t *testing.T) {
	ws := makeWorkspace("ws-no-relay", "default", v1.WorkspacePhaseCreating)
	ws.Status.PVCName = "workspace-ws-no-relay"
	pvc := makeBoundPVC("workspace-ws-no-relay", "default", ws.UID)
	pw := makePasswordSecret("ws-no-relay", "default")
	rte := makeRuntimeEnv("python-3.11")

	r := reconcilerFor(t, ws, pvc, pw, rte)
	r.InferenceRelayURL = ""

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)
	require.NotNil(t, pod)

	main := mainContainer(pod)
	require.NotNil(t, main)
	for _, e := range main.Env {
		assert.NotEqual(t, "INFERENCE_RELAY_BASEURL", e.Name,
			"INFERENCE_RELAY_BASEURL must not be set when InferenceRelayURL is empty")
	}
}

// TestBuildPod_RelayBaseURL_Injected verifies that when InferenceRelayURL is
// set, the env var is the plain URL (no embedded secret — URL obscurity suffices).
func TestBuildPod_RelayBaseURL_Injected(t *testing.T) {
	ws := makeWorkspace("ws-relay", "default", v1.WorkspacePhaseCreating)
	ws.Status.PVCName = "workspace-ws-relay"
	pvc := makeBoundPVC("workspace-ws-relay", "default", ws.UID)
	pw := makePasswordSecret("ws-relay", "default")
	rte := makeRuntimeEnv("python-3.11")

	r := reconcilerFor(t, ws, pvc, pw, rte)
	r.InferenceRelayURL = "https://relay.safespaces.dev"

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	main := mainContainer(pod)
	require.NotNil(t, main)
	assert.Equal(t, "https://relay.safespaces.dev", getEnv(main, "INFERENCE_RELAY_BASEURL"))
}

// --- helpers ---

func mainContainer(pod *corev1.Pod) *corev1.Container {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == "workspace" {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}

func getEnv(c *corev1.Container, name string) string {
	for _, e := range c.Env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}
