// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

// pod_builder_test.go — regression tests for workspace pod construction.
//
// Each test in this file pins one behavioral assertion about the pod spec
// produced by buildPod(). Tests are named after the worklog/epic that
// introduced the requirement they guard.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func newWorkspaceForPodBuilder(t *testing.T) *v1.Workspace {
	t.Helper()
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ws-pod-builder-test",
			Namespace: "default",
		},
		Spec: v1.WorkspaceSpec{
			Runtime: "ghcr.io/lenaxia/llmsafespace/runtimes/base:test",
		},
		Status: v1.WorkspaceStatus{
			PVCName: "pvc-pod-builder-test",
		},
	}
}

// TestPodBuilder_ContainerEnv_RequiredVars checks that the workspace container
// includes the minimum set of env vars needed for the agent to function.
func TestPodBuilder_ContainerEnv_RequiredVars(t *testing.T) {
	ws := newWorkspaceForPodBuilder(t)
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	var mainEnv map[string]string
	for _, c := range pod.Spec.Containers {
		if c.Name == "workspace" {
			mainEnv = make(map[string]string, len(c.Env))
			for _, e := range c.Env {
				mainEnv[e.Name] = e.Value
			}
			break
		}
	}
	require.NotNil(t, mainEnv, "workspace container not found in pod spec")

	assert.Equal(t, ws.Name, mainEnv["WORKSPACE_ID"])
	assert.NotEmpty(t, mainEnv["WORKSPACE_DIR"])
}

// TestPodBuilder_ContainerEnv_OpenCodeExperimentalEventSystem is the regression
// test for the context-usage "0/Unknown" bug (worklog 0263).
//
// Root cause: OPENCODE_EXPERIMENTAL_EVENT_SYSTEM was not set in the workspace pod
// env, so opencode never emitted session.next.step.ended to the /event SSE stream.
// The API proxy's persistContextFromEvent was therefore never called, leaving
// session_index.context_used NULL for every session and the Sidebar showing "0/Unknown".
//
// Fix: set OPENCODE_EXPERIMENTAL_EVENT_SYSTEM=true unconditionally in all workspace pods.
//
// Proven by live cluster experiment (worklog 0263): adding the flag to /tmp/secrets-env
// and restarting opencode caused context_used to be written to session_index within one
// second of the next LLM step completing (114422 tokens, exact match with
// input + cache.read + cache.write from the step.ended event).
func TestPodBuilder_ContainerEnv_OpenCodeExperimentalEventSystem(t *testing.T) {
	ws := newWorkspaceForPodBuilder(t)
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	var found bool
	for _, c := range pod.Spec.Containers {
		if c.Name != "workspace" {
			continue
		}
		for _, e := range c.Env {
			if e.Name == "OPENCODE_EXPERIMENTAL_EVENT_SYSTEM" {
				assert.Equal(t, "true", e.Value,
					"OPENCODE_EXPERIMENTAL_EVENT_SYSTEM must be 'true' — "+
						"without it opencode never emits step.ended and context_used is never written to DB")
				found = true
			}
		}
	}
	assert.True(t, found,
		"OPENCODE_EXPERIMENTAL_EVENT_SYSTEM must be present in the workspace container env — "+
			"it is required for the context usage bar to display real values")
}
