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

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

func newWorkspaceForPodBuilder(t *testing.T) *v1.Workspace {
	t.Helper()
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ws-pod-builder-test",
			Namespace: "default",
		},
		Spec: v1.WorkspaceSpec{
			Runtime: "ghcr.io/lenaxia/llmsafespaces/runtimes/base:test",
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

// TestPodBuilder_ReadinessProbe_TightTiming verifies the readiness probe
// is configured for fast pod-Ready detection (cold-start optimization,
// 2026-06-23 perf audit).
//
// Pre-fix: InitialDelaySeconds=10, PeriodSeconds=15 — kubelet would wait
// 10s before probing, then poll every 15s. The agent reaches /v1/readyz=200
// at roughly T+22s after PodScheduled, so on a bad probe-phase alignment
// the pod could remain "not Ready" for an additional 5–13s after the agent
// was actually ready.
//
// Post-fix: InitialDelaySeconds=2, PeriodSeconds=2 — overall ready-detection
// budget is similar (FailureThreshold raised to 30 → 60s tolerance) but
// post-readyz-200 latency drops to a single 2s tick.
func TestPodBuilder_ReadinessProbe_TightTiming(t *testing.T) {
	ws := newWorkspaceForPodBuilder(t)
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	require.Len(t, pod.Spec.Containers, 1, "expected one main container")
	probe := pod.Spec.Containers[0].ReadinessProbe
	require.NotNil(t, probe, "readiness probe must be set")

	assert.Equal(t, int32(2), probe.InitialDelaySeconds,
		"InitialDelaySeconds must be 2s — kubelet should start probing quickly so "+
			"a cold-started agent transitions to Ready within one poll period")
	assert.Equal(t, int32(2), probe.PeriodSeconds,
		"PeriodSeconds must be 2s — readiness checks must align tightly to "+
			"agent /v1/readyz=200 to minimize post-ready dead time")
	assert.Equal(t, int32(2), probe.TimeoutSeconds,
		"TimeoutSeconds must be 2s — /v1/readyz is cache-backed, sub-50ms in "+
			"the steady state, so 2s is a generous failure budget")
	assert.Equal(t, int32(30), probe.FailureThreshold,
		"FailureThreshold must be 30 — preserves 60s total ready budget at 2s period")
}

// TestPodBuilder_StartupProbe_FastDetection verifies the startup probe
// allows aggressive cold-start polling without affecting steady-state
// liveness behavior (2026-06-23 perf audit).
//
// Why a separate startup probe: kubelet runs only one probe at a time
// per container — when the startup probe is set, liveness and readiness
// probes are paused until startup succeeds. This lets us probe at 1s
// intervals during boot without paying the cost on every steady-state
// liveness check.
func TestPodBuilder_StartupProbe_FastDetection(t *testing.T) {
	ws := newWorkspaceForPodBuilder(t)
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	probe := pod.Spec.Containers[0].StartupProbe
	require.NotNil(t, probe, "startup probe must be set so the readiness probe is "+
		"unblocked the moment agentd reports ready, not on the next poll cycle")
	require.NotNil(t, probe.HTTPGet, "startup probe must use HTTP, matching readiness")
	assert.Equal(t, "/v1/readyz", probe.HTTPGet.Path,
		"startup probe path must match readiness — same gate, faster cadence")
	assert.Equal(t, int32(1), probe.PeriodSeconds,
		"PeriodSeconds=1 — probe every second during boot")
	assert.GreaterOrEqual(t, probe.FailureThreshold, int32(60),
		"FailureThreshold must be >=60 to give the relay-injector restart cycle (~30s) "+
			"plus a safety margin before the pod is killed")
}

// TestPodBuilder_LivenessProbe_StableTiming pins the liveness probe to a
// gentle steady-state cadence. Liveness-probe failures kill the pod, so
// timeouts and failure thresholds must be conservative; the startup probe
// (above) handles the boot-time tightening.
func TestPodBuilder_LivenessProbe_StableTiming(t *testing.T) {
	ws := newWorkspaceForPodBuilder(t)
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	probe := pod.Spec.Containers[0].LivenessProbe
	require.NotNil(t, probe)
	require.NotNil(t, probe.HTTPGet)
	assert.Equal(t, "/v1/healthz", probe.HTTPGet.Path)
	// Period and threshold are deliberately gentle — liveness failures
	// kill the pod, so we want lots of slack against transient network
	// or overload conditions.
	assert.GreaterOrEqual(t, probe.PeriodSeconds, int32(10))
	assert.GreaterOrEqual(t, probe.FailureThreshold, int32(3))
}

// TestPodBuilder_TerminationGracePeriod_Tight verifies the pod's
// terminationGracePeriodSeconds is set to a tight value (2026-06-23 perf
// audit, item #5). The default kubelet value is 30s, but agentd has been
// measured to exit cleanly in under 1s on the live cluster — the headroom
// was unused.
//
// Concrete impact: on every controller-initiated pod recycle (suspend,
// restartGeneration bump, architecture drift, password-secret heal),
// this saves up to ~25s of dead time waiting for SIGKILL.
//
// Lower bound is 5s (not 1s) to leave room for opencode SIGTERM
// propagation by agentd's supervisor (managed_process.go reserves a
// 5s SIGTERM-then-SIGKILL window for the opencode child).
func TestPodBuilder_TerminationGracePeriod_Tight(t *testing.T) {
	ws := newWorkspaceForPodBuilder(t)
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	require.NotNil(t, pod.Spec.TerminationGracePeriodSeconds,
		"terminationGracePeriodSeconds must be set explicitly — "+
			"the default of 30s wastes ~25s on every pod termination")
	assert.GreaterOrEqual(t, *pod.Spec.TerminationGracePeriodSeconds, int64(5),
		"must allow >=5s for agentd to SIGTERM opencode and exit cleanly")
	assert.LessOrEqual(t, *pod.Spec.TerminationGracePeriodSeconds, int64(15),
		"must be tight enough that suspend/recycle latency benefits — "+
			"agentd exits in <1s in practice, 30s default was over-provisioned")
}
