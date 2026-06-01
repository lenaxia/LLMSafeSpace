package workspace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCalculateBackoff_FirstAttempt(t *testing.T) {
	policy := recoveryPolicies[FailureClassInfrastructure]
	backoff := calculateBackoff(1, policy)
	assert.Equal(t, 5*time.Second, backoff)
}

func TestCalculateBackoff_SecondAttempt(t *testing.T) {
	policy := recoveryPolicies[FailureClassInfrastructure]
	backoff := calculateBackoff(2, policy)
	assert.Equal(t, 10*time.Second, backoff)
}

func TestCalculateBackoff_CapsAtMax(t *testing.T) {
	policy := recoveryPolicies[FailureClassInfrastructure]
	backoff := calculateBackoff(10, policy)
	assert.Equal(t, policy.BackoffMax, backoff)
}

func TestCalculateBackoff_ZeroFailures(t *testing.T) {
	policy := recoveryPolicies[FailureClassInfrastructure]
	backoff := calculateBackoff(0, policy)
	assert.Equal(t, time.Duration(0), backoff)
}

func TestCalculateBackoff_HighFailureCount_NoOverflow(t *testing.T) {
	// F39: ConsecutiveFailures=100 → BackoffMax, no negative duration
	policy := recoveryPolicies[FailureClassProcess]
	backoff := calculateBackoff(100, policy)
	assert.Equal(t, policy.BackoffMax, backoff)
	assert.True(t, backoff > 0, "backoff must be positive")
}

func TestCalculateBackoff_ShiftCappedAt30(t *testing.T) {
	policy := RecoveryPolicy{
		BackoffBase: 1 * time.Second,
		BackoffMax:  1 * time.Hour,
	}
	// failures=32 → shift=31 → capped at 30
	backoff := calculateBackoff(32, policy)
	assert.True(t, backoff > 0)
	assert.True(t, backoff <= policy.BackoffMax)
}

func TestRecoveryPolicy_InfrastructureNeverSafeMode(t *testing.T) {
	policy := recoveryPolicies[FailureClassInfrastructure]
	assert.Equal(t, int32(0), policy.SafeModeAfter)
}

func TestRecoveryPolicy_ProcessSafeModeAfter6(t *testing.T) {
	policy := recoveryPolicies[FailureClassProcess]
	assert.Equal(t, int32(6), policy.SafeModeAfter)
}

func TestRecoveryPolicy_ConfigurationSafeModeAfter3(t *testing.T) {
	policy := recoveryPolicies[FailureClassConfiguration]
	assert.Equal(t, int32(3), policy.SafeModeAfter)
}

func TestShouldEnterSafeMode_InfraAtHighCount_Never(t *testing.T) {
	// F34: Infrastructure never triggers safe mode regardless of count
	policy := recoveryPolicies[FailureClassInfrastructure]
	assert.False(t, shouldEnterSafeMode(50, policy))
}

func TestShouldEnterSafeMode_ProcessAt6(t *testing.T) {
	policy := recoveryPolicies[FailureClassProcess]
	assert.False(t, shouldEnterSafeMode(5, policy))
	assert.True(t, shouldEnterSafeMode(6, policy))
	assert.True(t, shouldEnterSafeMode(7, policy))
}

func TestShouldEnterSafeMode_ConfigAt3(t *testing.T) {
	policy := recoveryPolicies[FailureClassConfiguration]
	assert.False(t, shouldEnterSafeMode(2, policy))
	assert.True(t, shouldEnterSafeMode(3, policy))
}
