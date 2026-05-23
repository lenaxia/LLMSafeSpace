package common

import (
	"testing"
	"time"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ---------------------------------------------------------------------------
// Condition helpers (utils.go)
// ---------------------------------------------------------------------------

func TestFindCondition_Found(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
		{Type: "PodRunning", Status: metav1.ConditionFalse},
	}
	c := FindCondition(conditions, "Ready")
	require.NotNil(t, c)
	assert.Equal(t, "Ready", c.Type)
}

func TestFindCondition_NotFound(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
	}
	assert.Nil(t, FindCondition(conditions, "Nonexistent"))
}

func TestFindCondition_EmptySlice(t *testing.T) {
	assert.Nil(t, FindCondition(nil, "Ready"))
}

func TestSetCondition_AddsNewCondition(t *testing.T) {
	conditions := []metav1.Condition{}
	SetCondition(&conditions, "Ready", metav1.ConditionTrue, "PodReady", "pod is ready")

	require.Len(t, conditions, 1)
	assert.Equal(t, "Ready", conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, conditions[0].Status)
	assert.Equal(t, "PodReady", conditions[0].Reason)
	assert.Equal(t, "pod is ready", conditions[0].Message)
}

func TestSetCondition_UpdatesExistingCondition(t *testing.T) {
	now := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	conditions := []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "NotReady",
			Message:            "pod not ready",
			LastTransitionTime: now,
		},
	}

	SetCondition(&conditions, "Ready", metav1.ConditionTrue, "PodReady", "pod is ready")

	require.Len(t, conditions, 1)
	assert.Equal(t, metav1.ConditionTrue, conditions[0].Status)
	assert.Equal(t, "PodReady", conditions[0].Reason)
	// LastTransitionTime should be updated because status changed
	assert.True(t, conditions[0].LastTransitionTime.After(now.Time))
}

func TestSetCondition_NoTransitionTimeUpdateWhenStatusUnchanged(t *testing.T) {
	original := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	conditions := []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "OldReason",
			LastTransitionTime: original,
		},
	}

	SetCondition(&conditions, "Ready", metav1.ConditionTrue, "NewReason", "updated message")

	require.Len(t, conditions, 1)
	// Status unchanged — LastTransitionTime must not be updated
	assert.Equal(t, original.Time.Unix(), conditions[0].LastTransitionTime.Time.Unix())
	assert.Equal(t, "NewReason", conditions[0].Reason)
}

func TestSetCondition_MultipleConditions(t *testing.T) {
	conditions := []metav1.Condition{}
	SetCondition(&conditions, "Ready", metav1.ConditionTrue, "R1", "m1")
	SetCondition(&conditions, "PodRunning", metav1.ConditionFalse, "R2", "m2")

	assert.Len(t, conditions, 2)
	assert.NotNil(t, FindCondition(conditions, "Ready"))
	assert.NotNil(t, FindCondition(conditions, "PodRunning"))
}

func TestIsConditionTrue_True(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
	}
	assert.True(t, IsConditionTrue(conditions, "Ready"))
}

func TestIsConditionTrue_False(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionFalse},
	}
	assert.False(t, IsConditionTrue(conditions, "Ready"))
}

func TestIsConditionTrue_Unknown(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionUnknown},
	}
	assert.False(t, IsConditionTrue(conditions, "Ready"))
}

func TestIsConditionTrue_Missing(t *testing.T) {
	assert.False(t, IsConditionTrue(nil, "Ready"))
}

// ---------------------------------------------------------------------------
// Finalizer helpers (utils.go)
// ---------------------------------------------------------------------------

// minimalObject is a minimal client.Object for testing finalizer operations.
// Using a real Sandbox CRD object avoids the need for a full fake client.
func makeSandbox() *v1.Sandbox {
	return &v1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
		},
	}
}

func TestAddFinalizer_AddsWhenAbsent(t *testing.T) {
	sb := makeSandbox()
	added := AddFinalizer(sb, SandboxFinalizer)
	assert.True(t, added)
	assert.Contains(t, sb.Finalizers, SandboxFinalizer)
}

func TestAddFinalizer_NoOpWhenAlreadyPresent(t *testing.T) {
	sb := makeSandbox()
	sb.Finalizers = []string{SandboxFinalizer}
	added := AddFinalizer(sb, SandboxFinalizer)
	assert.False(t, added)
	// Still exactly one copy
	assert.Equal(t, 1, countFinalizer(sb, SandboxFinalizer))
}

func TestRemoveFinalizer_RemovesWhenPresent(t *testing.T) {
	sb := makeSandbox()
	sb.Finalizers = []string{SandboxFinalizer}
	removed := RemoveFinalizer(sb, SandboxFinalizer)
	assert.True(t, removed)
	assert.NotContains(t, sb.Finalizers, SandboxFinalizer)
}

func TestRemoveFinalizer_NoOpWhenAbsent(t *testing.T) {
	sb := makeSandbox()
	removed := RemoveFinalizer(sb, SandboxFinalizer)
	assert.False(t, removed)
}

func countFinalizer(obj client.Object, finalizer string) int {
	count := 0
	for _, f := range obj.GetFinalizers() {
		if f == finalizer {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------------------
// IsPodReady (utils.go)
// ---------------------------------------------------------------------------

func TestIsPodReady_RunningWithReadyCondition(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	assert.True(t, IsPodReady(pod))
}

func TestIsPodReady_RunningWithoutReadyCondition(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{},
		},
	}
	assert.False(t, IsPodReady(pod))
}

func TestIsPodReady_RunningReadyFalse(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			},
		},
	}
	assert.False(t, IsPodReady(pod))
}

func TestIsPodReady_NotRunning(t *testing.T) {
	for _, phase := range []corev1.PodPhase{
		corev1.PodPending, corev1.PodSucceeded, corev1.PodFailed, corev1.PodUnknown,
	} {
		pod := &corev1.Pod{
			Status: corev1.PodStatus{
				Phase: phase,
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
				},
			},
		}
		assert.False(t, IsPodReady(pod), "phase %s should not be ready", phase)
	}
}

// ---------------------------------------------------------------------------
// GenerateRandomString (utils.go)
// ---------------------------------------------------------------------------

func TestGenerateRandomString_Length(t *testing.T) {
	for _, n := range []int{1, 5, 10, 15} {
		result := GenerateRandomString(n)
		assert.LessOrEqual(t, len(result), n, "length=%d", n)
	}
}

func TestGenerateRandomString_NotEmpty(t *testing.T) {
	assert.NotEmpty(t, GenerateRandomString(10))
}

// ---------------------------------------------------------------------------
// ConvertToMetaV1Condition / ConvertFromMetaV1Condition (condition_adapter.go)
// ---------------------------------------------------------------------------

func TestConvertToMetaV1Condition_FieldMapping(t *testing.T) {
	sc := v1.SandboxCondition{
		Type:    "Ready",
		Status:  "True",
		Reason:  "PodRunning",
		Message: "pod is running",
	}

	mc := ConvertToMetaV1Condition(sc)

	assert.Equal(t, "Ready", mc.Type)
	assert.Equal(t, metav1.ConditionStatus("True"), mc.Status)
	assert.Equal(t, "PodRunning", mc.Reason)
	assert.Equal(t, "pod is running", mc.Message)
	// LastTransitionTime is set by the function
	assert.False(t, mc.LastTransitionTime.IsZero())
}

func TestConvertFromMetaV1Condition_FieldMapping(t *testing.T) {
	mc := metav1.Condition{
		Type:    "PodCreated",
		Status:  metav1.ConditionTrue,
		Reason:  "Created",
		Message: "pod created successfully",
	}

	sc := ConvertFromMetaV1Condition(mc)

	assert.Equal(t, "PodCreated", sc.Type)
	assert.Equal(t, "True", sc.Status)
	assert.Equal(t, "Created", sc.Reason)
	assert.Equal(t, "pod created successfully", sc.Message)
}

func TestConvertRoundTrip(t *testing.T) {
	original := v1.SandboxCondition{
		Type:    "Ready",
		Status:  "False",
		Reason:  "PodNotRunning",
		Message: "pod crashed",
	}

	// SandboxCondition → metav1.Condition → SandboxCondition
	roundTripped := ConvertFromMetaV1Condition(ConvertToMetaV1Condition(original))

	assert.Equal(t, original.Type, roundTripped.Type)
	assert.Equal(t, original.Status, roundTripped.Status)
	assert.Equal(t, original.Reason, roundTripped.Reason)
	assert.Equal(t, original.Message, roundTripped.Message)
}

func TestConvertToMetaV1ConditionArray_Empty(t *testing.T) {
	result := ConvertToMetaV1ConditionArray(nil)
	assert.Len(t, result, 0)
}

func TestConvertFromMetaV1ConditionArray_Empty(t *testing.T) {
	result := ConvertFromMetaV1ConditionArray(nil)
	assert.Len(t, result, 0)
}

func TestConvertToMetaV1ConditionArray_Multiple(t *testing.T) {
	in := []v1.SandboxCondition{
		{Type: "A", Status: "True"},
		{Type: "B", Status: "False"},
	}
	out := ConvertToMetaV1ConditionArray(in)
	require.Len(t, out, 2)
	assert.Equal(t, "A", out[0].Type)
	assert.Equal(t, "B", out[1].Type)
}

// ---------------------------------------------------------------------------
// SetSandboxCondition (condition_adapter.go)
// ---------------------------------------------------------------------------

func TestSetSandboxCondition_AddsNew(t *testing.T) {
	conditions := []v1.SandboxCondition{}
	SetSandboxCondition(&conditions, "Ready", "True", "PodReady", "pod is running")

	require.Len(t, conditions, 1)
	assert.Equal(t, "Ready", conditions[0].Type)
	assert.Equal(t, "True", conditions[0].Status)
}

func TestSetSandboxCondition_UpdatesExisting(t *testing.T) {
	conditions := []v1.SandboxCondition{
		{Type: "Ready", Status: "False", Reason: "Init", Message: "initializing"},
	}
	SetSandboxCondition(&conditions, "Ready", "True", "PodReady", "pod is running")

	require.Len(t, conditions, 1)
	assert.Equal(t, "True", conditions[0].Status)
	assert.Equal(t, "PodReady", conditions[0].Reason)
}
