package workspace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func TestMaybeResetConsecutiveFailures_After2Min(t *testing.T) {
	ws := &v1.Workspace{}
	twoMinAgo := metav1.NewTime(time.Now().Add(-3 * time.Minute))
	ws.Status.LastStableAt = &twoMinAgo
	ws.Status.ConsecutiveFailures = 5
	ws.Status.LastFailureClass = string(FailureClassProcess)

	maybeResetConsecutiveFailures(ws)

	assert.Equal(t, int32(0), ws.Status.ConsecutiveFailures)
	assert.Equal(t, "", ws.Status.LastFailureClass)
	assert.Nil(t, ws.Status.LastFailureAt)
	assert.Nil(t, ws.Status.NextRetryAt)
}

func TestMaybeResetConsecutiveFailures_Before2Min(t *testing.T) {
	ws := &v1.Workspace{}
	oneMinAgo := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	ws.Status.LastStableAt = &oneMinAgo
	ws.Status.ConsecutiveFailures = 5

	maybeResetConsecutiveFailures(ws)

	assert.Equal(t, int32(5), ws.Status.ConsecutiveFailures)
}

func TestMaybeResetConsecutiveFailures_NilLastStableAt_StartsClock(t *testing.T) {
	ws := &v1.Workspace{}
	ws.Status.ConsecutiveFailures = 3
	ws.Status.LastStableAt = nil

	maybeResetConsecutiveFailures(ws)

	// Should set LastStableAt to now (start the clock)
	assert.NotNil(t, ws.Status.LastStableAt)
	assert.Equal(t, int32(3), ws.Status.ConsecutiveFailures) // not reset yet
}

func TestMaybeResetConsecutiveFailures_ZeroFailures_NoOp(t *testing.T) {
	ws := &v1.Workspace{}
	ws.Status.ConsecutiveFailures = 0

	maybeResetConsecutiveFailures(ws)

	assert.Nil(t, ws.Status.LastStableAt)
}

func TestNextRetryAtEnforcement(t *testing.T) {
	future := metav1.NewTime(time.Now().Add(30 * time.Second))
	ws := &v1.Workspace{}
	ws.Status.NextRetryAt = &future

	remaining := timeUntilNextRetry(ws)
	assert.True(t, remaining > 0)
	assert.True(t, remaining <= 30*time.Second)
}

func TestNextRetryAtEnforcement_Elapsed(t *testing.T) {
	past := metav1.NewTime(time.Now().Add(-10 * time.Second))
	ws := &v1.Workspace{}
	ws.Status.NextRetryAt = &past

	remaining := timeUntilNextRetry(ws)
	assert.Equal(t, time.Duration(0), remaining)
}

func TestNextRetryAtEnforcement_Nil(t *testing.T) {
	ws := &v1.Workspace{}
	ws.Status.NextRetryAt = nil

	remaining := timeUntilNextRetry(ws)
	assert.Equal(t, time.Duration(0), remaining)
}
