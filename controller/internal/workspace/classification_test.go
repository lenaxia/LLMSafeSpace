package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestClassifyFailure_PodNotFound(t *testing.T) {
	obs := PodObservation{Exists: false}
	assert.Equal(t, FailureClassInfrastructure, classifyFailure(obs))
}

func TestClassifyFailure_OOMKilled(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodFailed, ContainerOOMKilled: true}
	assert.Equal(t, FailureClassResource, classifyFailure(obs))
}

func TestClassifyFailure_OOMKilled_TakesPriority(t *testing.T) {
	// OOM + Evicted → Resource (OOM wins)
	obs := PodObservation{Exists: true, Phase: corev1.PodFailed, Reason: "Evicted", ContainerOOMKilled: true}
	assert.Equal(t, FailureClassResource, classifyFailure(obs))
}

func TestClassifyFailure_Evicted(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodFailed, Reason: "Evicted"}
	assert.Equal(t, FailureClassInfrastructure, classifyFailure(obs))
}

func TestClassifyFailure_Preempting(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodFailed, Reason: "Preempting"}
	assert.Equal(t, FailureClassInfrastructure, classifyFailure(obs))
}

func TestClassifyFailure_NodeShutdown(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodFailed, Reason: "NodeShutdown"}
	assert.Equal(t, FailureClassInfrastructure, classifyFailure(obs))
}

func TestClassifyFailure_Unschedulable(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodPending, Unschedulable: true}
	assert.Equal(t, FailureClassInfrastructure, classifyFailure(obs))
}

func TestClassifyFailure_ImagePullBackOff(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodPending, ContainerReason: "ImagePullBackOff"}
	assert.Equal(t, FailureClassConfiguration, classifyFailure(obs))
}

func TestClassifyFailure_ErrImagePull(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodPending, ContainerReason: "ErrImagePull"}
	assert.Equal(t, FailureClassConfiguration, classifyFailure(obs))
}

func TestClassifyFailure_CreateContainerConfigError(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodPending, ContainerReason: "CreateContainerConfigError"}
	assert.Equal(t, FailureClassConfiguration, classifyFailure(obs))
}

func TestClassifyFailure_CrashLoopBackOff(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodRunning, ContainerReason: "CrashLoopBackOff"}
	assert.Equal(t, FailureClassProcess, classifyFailure(obs))
}

func TestClassifyFailure_CrashLoop_Flag(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodRunning, CrashLoop: true}
	assert.Equal(t, FailureClassProcess, classifyFailure(obs))
}

func TestClassifyFailure_RunContainerError(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodFailed, ContainerReason: "RunContainerError"}
	assert.Equal(t, FailureClassProcess, classifyFailure(obs))
}

func TestClassifyFailure_UnknownReason_DefaultsToProcess(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodFailed, Reason: "SomethingWeird"}
	assert.Equal(t, FailureClassProcess, classifyFailure(obs))
}

func TestClassifyFailure_NeverReturnsNone(t *testing.T) {
	// Even with minimal observation, should never return empty
	obs := PodObservation{Exists: true, Phase: corev1.PodFailed}
	result := classifyFailure(obs)
	assert.NotEqual(t, FailureClass(""), result)
	assert.Equal(t, FailureClassProcess, result)
}

func TestClassifyFailure_DisruptionTarget(t *testing.T) {
	obs := PodObservation{Exists: true, Phase: corev1.PodFailed, Reason: "DisruptionTarget"}
	assert.Equal(t, FailureClassInfrastructure, classifyFailure(obs))
}

func TestObservePod_NilPod(t *testing.T) {
	obs := observePod(nil)
	assert.False(t, obs.Exists)
}

func TestObservePod_RunningPod(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			},
		},
	}
	obs := observePod(pod)
	assert.True(t, obs.Exists)
	assert.Equal(t, corev1.PodRunning, obs.Phase)
	assert.True(t, obs.Scheduled)
	assert.False(t, obs.CrashLoop)
}

func TestObservePod_CrashLoopPod(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
					},
				},
			},
		},
	}
	obs := observePod(pod)
	assert.True(t, obs.CrashLoop)
	assert.Equal(t, "CrashLoopBackOff", obs.ContainerReason)
}

func TestObservePod_OOMKilledPod(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason:   "OOMKilled",
							ExitCode: 137,
						},
					},
				},
			},
		},
	}
	obs := observePod(pod)
	assert.True(t, obs.ContainerOOMKilled)
	assert.Equal(t, int32(137), obs.ContainerExitCode)
}
