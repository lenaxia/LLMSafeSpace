// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// ---- per-test metric factories ----
// Each test creates fresh prometheus metric objects so observations from one
// test cannot bleed into another. The global package-level vars in
// controller/internal/metrics are only used by production code paths.

func newTestCreateHist() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "test_workspace_create_duration_seconds",
		Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
	}, []string{"has_packages", "has_init_script"})
}

func newTestResumeHist() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "test_workspace_resume_duration_seconds",
		Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
	}, []string{"resume_type"})
}

func newTestInitHist() prometheus.Histogram {
	return prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "test_workspace_init_container_duration_seconds",
		Buckets: []float64{0.5, 1, 5, 10, 30, 60},
	})
}

// ---- gathering helpers ----

func gatherCount(t *testing.T, hist *prometheus.HistogramVec, labels prometheus.Labels) uint64 {
	t.Helper()
	m := &dto.Metric{}
	require.NoError(t, hist.With(labels).(prometheus.Histogram).Write(m))
	return m.GetHistogram().GetSampleCount()
}

func gatherSimpleCount(t *testing.T, hist prometheus.Histogram) uint64 {
	t.Helper()
	m := &dto.Metric{}
	require.NoError(t, hist.Write(m))
	return m.GetHistogram().GetSampleCount()
}

func gatherSimpleSum(t *testing.T, hist prometheus.Histogram) float64 {
	t.Helper()
	m := &dto.Metric{}
	require.NoError(t, hist.Write(m))
	return m.GetHistogram().GetSampleSum()
}

// ---- workspace builder helpers ----

func makeWorkspaceWithPackages(packages bool, initScript bool) *v1.Workspace {
	ws := makeWorkspace("ws-pkg", "default", v1.WorkspacePhaseCreating)
	if packages {
		ws.Spec.Packages = []v1.WorkspacePackageSet{
			{Runtime: "python3", Requirements: []string{"numpy"}},
		}
	}
	if initScript {
		ws.Spec.InitScript = "echo hello"
	}
	return ws
}

func makeRunningPodWithInitStatus(startedAt, finishedAt time.Time) *corev1.Pod {
	pod := makeRunningPod("pod-init", "default", "10.0.0.1")
	pod.Status.InitContainerStatuses = []corev1.ContainerStatus{
		{
			Name: "workspace-setup",
			State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{
					StartedAt:  metav1.NewTime(startedAt),
					FinishedAt: metav1.NewTime(finishedAt),
				},
			},
		},
	}
	return pod
}

// ptrTime returns a pointer to a metav1.Time. Helper to reduce boilerplate.
func ptrTime(t metav1.Time) *metav1.Time { return &t }

// ---- PendingAt anchor tests ----

// TestHandlePendingSetsPendingAtFromAnnotation verifies that when
// AnnotationRequestedAt is present, PendingAt is set to the annotation value.
func TestHandlePendingSetsPendingAtFromAnnotation(t *testing.T) {
	// Use second-precision to match metav1.Time JSON round-trip behavior.
	requested := time.Now().Add(-3 * time.Second).Truncate(time.Second).UTC()
	ws := makeWorkspace("ws-ann", "default", v1.WorkspacePhasePending)
	ws.Annotations = map[string]string{
		v1.AnnotationRequestedAt: requested.Format(time.RFC3339Nano),
	}
	pvc := makeBoundPVC("workspace-ws-ann", "default", ws.UID)
	r := reconcilerFor(t, ws, pvc)
	_, err := r.handlePending(context.Background(), ws)
	require.NoError(t, err)

	require.NotNil(t, ws.Status.PendingAt, "PendingAt must be set after PVC binds")
	// Allow 1s rounding for RFC3339 second-precision storage.
	assert.WithinDuration(t, requested, ws.Status.PendingAt.Time, time.Second,
		"PendingAt should match AnnotationRequestedAt")
}

// TestHandlePendingSetsPendingAtFallbackToNow verifies fallback to ~now when
// annotation is absent.
func TestHandlePendingSetsPendingAtFallbackToNow(t *testing.T) {
	before := time.Now().Add(-time.Second)
	ws := makeWorkspace("ws-noann", "default", v1.WorkspacePhasePending)
	pvc := makeBoundPVC("workspace-ws-noann", "default", ws.UID)
	r := reconcilerFor(t, ws, pvc)
	_, err := r.handlePending(context.Background(), ws)
	require.NoError(t, err)

	require.NotNil(t, ws.Status.PendingAt)
	assert.True(t, ws.Status.PendingAt.After(before))
	assert.True(t, ws.Status.PendingAt.Time.Before(time.Now().Add(time.Second)))
}

// TestHandlePendingDoesNotOverwritePendingAt verifies idempotency on re-entry.
func TestHandlePendingDoesNotOverwritePendingAt(t *testing.T) {
	// Use second-precision to match metav1.Time JSON storage.
	original := metav1.NewTime(time.Now().Add(-10 * time.Second).Truncate(time.Second))
	ws := makeWorkspace("ws-idem", "default", v1.WorkspacePhasePending)
	ws.Status.PendingAt = &original
	pvc := makeBoundPVC("workspace-ws-idem", "default", ws.UID)
	r := reconcilerFor(t, ws, pvc)
	_, err := r.handlePending(context.Background(), ws)
	require.NoError(t, err)

	require.NotNil(t, ws.Status.PendingAt)
	assert.Equal(t, original.UTC(), ws.Status.PendingAt.UTC(),
		"PendingAt must not be overwritten on re-entry")
}

// ---- ResumedAt anchor tests ----

// TestHandleResumingSetsResumedAt verifies handleResuming sets ResumedAt.
func TestHandleResumingSetsResumedAt(t *testing.T) {
	before := time.Now().Add(-time.Second)
	ws := makeWorkspace("ws-res", "default", v1.WorkspacePhaseResuming)
	ws.Status.SuspendedAt = ptrTime(metav1.Now())
	r := reconcilerFor(t, ws)
	_, err := r.handleResuming(context.Background(), ws)
	require.NoError(t, err)

	require.NotNil(t, ws.Status.ResumedAt, "ResumedAt must be set by handleResuming")
	assert.True(t, ws.Status.ResumedAt.After(before))
	assert.True(t, ws.Status.ResumedAt.Time.Before(time.Now().Add(time.Second)))
}

// ---- recordStartupMetricsInto tests ----

// TestRecordStartupMetricsCreatePath verifies the create histogram gets one
// observation and PendingAt is cleared.
func TestRecordStartupMetricsCreatePath(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(false, false)
	ws.Status.PendingAt = ptrTime(metav1.NewTime(time.Now().Add(-5 * time.Second)))
	pod := makeRunningPod("pod-1", "default", "10.0.0.1")

	recordStartupMetricsInto(ws, pod, ch, rh, ih)

	assert.EqualValues(t, 1, gatherCount(t, ch, prometheus.Labels{"has_packages": "false", "has_init_script": "false"}))
	assert.Nil(t, ws.Status.PendingAt, "PendingAt must be cleared")
	assert.Nil(t, ws.Status.ResumedAt)
}

// TestRecordStartupMetricsResumePath verifies the resume histogram gets one
// observation and ResumedAt is cleared.
func TestRecordStartupMetricsResumePath(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(false, false)
	ws.Status.RestartCount = 2
	ws.Status.ResumedAt = ptrTime(metav1.NewTime(time.Now().Add(-15 * time.Second)))
	pod := makeRunningPod("pod-1", "default", "10.0.0.1")

	recordStartupMetricsInto(ws, pod, ch, rh, ih)

	assert.EqualValues(t, 1, gatherCount(t, rh, prometheus.Labels{"resume_type": "subsequent_resume"}))
	assert.Nil(t, ws.Status.ResumedAt, "ResumedAt must be cleared")
	assert.Nil(t, ws.Status.PendingAt)
}

// TestRecordStartupMetricsFirstResumeLabel verifies RestartCount==0 → "first_resume".
func TestRecordStartupMetricsFirstResumeLabel(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(false, false)
	ws.Status.RestartCount = 0
	ws.Status.ResumedAt = ptrTime(metav1.NewTime(time.Now().Add(-8 * time.Second)))

	recordStartupMetricsInto(ws, makeRunningPod("p", "default", "10.0.0.1"), ch, rh, ih)

	assert.EqualValues(t, 1, gatherCount(t, rh, prometheus.Labels{"resume_type": "first_resume"}))
	assert.EqualValues(t, 0, gatherCount(t, rh, prometheus.Labels{"resume_type": "subsequent_resume"}))
}

// TestRecordStartupMetricsHasPackagesLabel verifies the has_packages=true label path.
func TestRecordStartupMetricsHasPackagesLabel(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(true, false)
	ws.Status.PendingAt = ptrTime(metav1.NewTime(time.Now().Add(-30 * time.Second)))

	recordStartupMetricsInto(ws, makeRunningPod("p", "default", "10.0.0.1"), ch, rh, ih)

	assert.EqualValues(t, 1, gatherCount(t, ch, prometheus.Labels{"has_packages": "true", "has_init_script": "false"}))
	assert.EqualValues(t, 0, gatherCount(t, ch, prometheus.Labels{"has_packages": "false", "has_init_script": "false"}))
}

// TestRecordStartupMetricsBothLabelsTrue verifies has_packages=true, has_init_script=true.
func TestRecordStartupMetricsBothLabelsTrue(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(true, true)
	ws.Status.PendingAt = ptrTime(metav1.NewTime(time.Now().Add(-20 * time.Second)))

	recordStartupMetricsInto(ws, makeRunningPod("p", "default", "10.0.0.1"), ch, rh, ih)

	assert.EqualValues(t, 1, gatherCount(t, ch, prometheus.Labels{"has_packages": "true", "has_init_script": "true"}))
}

// TestRecordStartupMetricsStaleCreateAnchorDropped verifies an anchor older
// than maxStartupAnchorAge is NOT observed and IS cleared.
func TestRecordStartupMetricsStaleCreateAnchorDropped(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(false, false)
	stale := time.Now().Add(-(maxStartupAnchorAge + time.Second))
	ws.Status.PendingAt = ptrTime(metav1.NewTime(stale))

	recordStartupMetricsInto(ws, makeRunningPod("p", "default", "10.0.0.1"), ch, rh, ih)

	assert.EqualValues(t, 0, gatherCount(t, ch, prometheus.Labels{"has_packages": "false", "has_init_script": "false"}),
		"stale anchor must NOT be observed")
	assert.Nil(t, ws.Status.PendingAt, "stale PendingAt must still be cleared")
}

// TestRecordStartupMetricsStaleResumeAnchorDropped mirrors the above for resume.
func TestRecordStartupMetricsStaleResumeAnchorDropped(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(false, false)
	stale := time.Now().Add(-(maxStartupAnchorAge + time.Second))
	ws.Status.ResumedAt = ptrTime(metav1.NewTime(stale))

	recordStartupMetricsInto(ws, makeRunningPod("p", "default", "10.0.0.1"), ch, rh, ih)

	assert.EqualValues(t, 0, gatherCount(t, rh, prometheus.Labels{"resume_type": "subsequent_resume"}),
		"stale resume anchor must NOT be observed")
	assert.Nil(t, ws.Status.ResumedAt, "stale ResumedAt must still be cleared")
}

// TestRecordStartupMetricsNeitherAnchorSet verifies no panic and zero
// observations when neither anchor is set.
func TestRecordStartupMetricsNeitherAnchorSet(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(false, false)

	assert.NotPanics(t, func() {
		recordStartupMetricsInto(ws, makeRunningPod("p", "default", "10.0.0.1"), ch, rh, ih)
	})

	assert.EqualValues(t, 0, gatherCount(t, ch, prometheus.Labels{"has_packages": "false", "has_init_script": "false"}))
	assert.EqualValues(t, 0, gatherCount(t, rh, prometheus.Labels{"resume_type": "first_resume"}))
}

// TestRecordStartupMetricsIdempotent verifies that calling recordStartupMetricsInto
// twice only records one observation, because anchors are cleared on first call.
func TestRecordStartupMetricsIdempotent(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(false, false)
	ws.Status.PendingAt = ptrTime(metav1.NewTime(time.Now().Add(-5 * time.Second)))
	pod := makeRunningPod("p", "default", "10.0.0.1")

	recordStartupMetricsInto(ws, pod, ch, rh, ih)
	recordStartupMetricsInto(ws, pod, ch, rh, ih) // anchor is nil now

	assert.EqualValues(t, 1, gatherCount(t, ch, prometheus.Labels{"has_packages": "false", "has_init_script": "false"}),
		"must record exactly one observation even when called twice")
}

// TestRecordStartupMetricsResumePreemptsCreate verifies that when BOTH anchors
// are set (unexpected state), the resume path takes precedence and PendingAt is
// also cleared to prevent a later observation.
func TestRecordStartupMetricsResumePreemptsCreate(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(false, false)
	ws.Status.PendingAt = ptrTime(metav1.NewTime(time.Now().Add(-5 * time.Second)))
	ws.Status.ResumedAt = ptrTime(metav1.NewTime(time.Now().Add(-10 * time.Second)))

	recordStartupMetricsInto(ws, makeRunningPod("p", "default", "10.0.0.1"), ch, rh, ih)

	// Resume wins (switch case ordering)
	assert.EqualValues(t, 1, gatherCount(t, rh, prometheus.Labels{"resume_type": "first_resume"}))
	assert.EqualValues(t, 0, gatherCount(t, ch, prometheus.Labels{"has_packages": "false", "has_init_script": "false"}))
	// Both anchors cleared
	assert.Nil(t, ws.Status.ResumedAt)
	assert.Nil(t, ws.Status.PendingAt)
}

// ---- init container duration tests ----

// TestInitContainerDurationHappyPath verifies duration is derived from pod
// initContainerStatuses correctly.
func TestInitContainerDurationHappyPath(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	start := time.Now().Add(-7 * time.Second)
	finish := start.Add(7 * time.Second)
	ws := makeWorkspaceWithPackages(true, false)
	ws.Status.PendingAt = ptrTime(metav1.NewTime(time.Now().Add(-10 * time.Second)))
	pod := makeRunningPodWithInitStatus(start, finish)

	recordStartupMetricsInto(ws, pod, ch, rh, ih)

	assert.EqualValues(t, 1, gatherSimpleCount(t, ih))
	assert.InDelta(t, 7.0, gatherSimpleSum(t, ih), 0.1)
}

// TestInitContainerDurationAbsentWhenNotRun verifies no observation when the
// workspace-setup init container did not run.
func TestInitContainerDurationAbsentWhenNotRun(t *testing.T) {
	ch, rh, ih := newTestCreateHist(), newTestResumeHist(), newTestInitHist()
	ws := makeWorkspaceWithPackages(false, false)
	ws.Status.PendingAt = ptrTime(metav1.NewTime(time.Now().Add(-5 * time.Second)))

	recordStartupMetricsInto(ws, makeRunningPod("p", "default", "10.0.0.1"), ch, rh, ih)

	assert.EqualValues(t, 0, gatherSimpleCount(t, ih))
}

// TestInitContainerDurationNilPod verifies no panic with a nil pod.
func TestInitContainerDurationNilPod(t *testing.T) {
	assert.NotPanics(t, func() {
		d := initContainerDuration(nil, "workspace-setup")
		assert.Zero(t, d)
	})
}

// TestInitContainerDurationNegativeDeltaIgnored verifies clock-skew edge case
// (FinishedAt < StartedAt) returns 0.
func TestInitContainerDurationNegativeDeltaIgnored(t *testing.T) {
	now := time.Now()
	pod := makeRunningPodWithInitStatus(now, now.Add(-1*time.Second))
	d := initContainerDuration(pod, "workspace-setup")
	assert.Zero(t, d)
}

// TestInitContainerDurationMissingTimestamps verifies zero-value timestamps
// return 0.
func TestInitContainerDurationMissingTimestamps(t *testing.T) {
	pod := makeRunningPod("p", "default", "10.0.0.1")
	pod.Status.InitContainerStatuses = []corev1.ContainerStatus{
		{
			Name: "workspace-setup",
			State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{
					// StartedAt and FinishedAt are zero
				},
			},
		},
	}
	d := initContainerDuration(pod, "workspace-setup")
	assert.Zero(t, d)
}

// TestInitContainerDurationWrongName verifies that a pod with only a
// differently-named init container returns 0.
func TestInitContainerDurationWrongName(t *testing.T) {
	start := time.Now().Add(-5 * time.Second)
	pod := makeRunningPodWithInitStatus(start, time.Now())
	// Use a container name that explicitly does not exist in the
	// workspace pod spec, so the lookup must miss and return 0.
	// "credential-setup" is the real sibling init container; using it
	// here would conflate "name doesn't match the target" with "the
	// other real init container is at index 0", which obscures intent.
	pod.Status.InitContainerStatuses[0].Name = "nonexistent-container"
	d := initContainerDuration(pod, "workspace-setup")
	assert.Zero(t, d)
}
