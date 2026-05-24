package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func makeWorkspace() *v1.Workspace {
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-workspace",
			Namespace: "default",
		},
	}
}

func TestAddFinalizer_AddsWhenAbsent(t *testing.T) {
	ws := makeWorkspace()
	added := AddFinalizer(ws, "test-finalizer")
	assert.True(t, added)
	assert.Contains(t, ws.Finalizers, "test-finalizer")
}

func TestAddFinalizer_NoOpWhenAlreadyPresent(t *testing.T) {
	ws := makeWorkspace()
	ws.Finalizers = []string{"test-finalizer"}
	added := AddFinalizer(ws, "test-finalizer")
	assert.False(t, added)
	assert.Equal(t, 1, len(ws.Finalizers))
}

func TestRemoveFinalizer_RemovesWhenPresent(t *testing.T) {
	ws := makeWorkspace()
	ws.Finalizers = []string{"test-finalizer"}
	removed := RemoveFinalizer(ws, "test-finalizer")
	assert.True(t, removed)
	assert.NotContains(t, ws.Finalizers, "test-finalizer")
}

func TestRemoveFinalizer_NoOpWhenAbsent(t *testing.T) {
	ws := makeWorkspace()
	removed := RemoveFinalizer(ws, "test-finalizer")
	assert.False(t, removed)
}

func TestSetCondition_AddsNewCondition(t *testing.T) {
	conditions := []metav1.Condition{}
	SetCondition(&conditions, "Ready", metav1.ConditionTrue, "AllGood", "Everything is fine")
	assert.Len(t, conditions, 1)
	assert.Equal(t, "Ready", conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, conditions[0].Status)
}

func TestSetCondition_UpdatesExistingCondition(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotReady"},
	}
	SetCondition(&conditions, "Ready", metav1.ConditionTrue, "AllGood", "Now ready")
	assert.Len(t, conditions, 1)
	assert.Equal(t, metav1.ConditionTrue, conditions[0].Status)
	assert.Equal(t, "AllGood", conditions[0].Reason)
}

func TestFindCondition_Found(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
	}
	c := FindCondition(conditions, "Ready")
	assert.NotNil(t, c)
	assert.Equal(t, metav1.ConditionTrue, c.Status)
}

func TestFindCondition_NotFound(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
	}
	c := FindCondition(conditions, "Missing")
	assert.Nil(t, c)
}

func TestIsConditionTrue(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
	}
	assert.True(t, IsConditionTrue(conditions, "Ready"))
	assert.False(t, IsConditionTrue(conditions, "Missing"))
}

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

func TestIsPodReady_NotRunning(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	assert.False(t, IsPodReady(pod))
}

func TestGenerateRandomString_Length(t *testing.T) {
	s := GenerateRandomString(32)
	assert.NotEmpty(t, s)
	assert.LessOrEqual(t, len(s), 32)
}

func TestGenerateRandomString_NotEmpty(t *testing.T) {
	s := GenerateRandomString(16)
	assert.NotEmpty(t, s)
}
