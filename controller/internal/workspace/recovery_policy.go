package workspace

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

type RecoveryPolicy struct {
	MaxAttempts    int
	BackoffBase    time.Duration
	BackoffMax     time.Duration
	BackoffFactor  int
	StabilityReset time.Duration
	SafeModeAfter  int32
}

var recoveryPolicies = map[FailureClass]RecoveryPolicy{
	FailureClassInfrastructure: {0, 5 * time.Second, 2 * time.Minute, 2, 2 * time.Minute, 0},
	FailureClassResource:       {0, 10 * time.Second, 5 * time.Minute, 2, 2 * time.Minute, 6},
	FailureClassProcess:        {0, 10 * time.Second, 5 * time.Minute, 2, 2 * time.Minute, 6},
	FailureClassConfiguration:  {0, 30 * time.Second, 5 * time.Minute, 2, 2 * time.Minute, 3},
}

func calculateBackoff(failures int32, policy RecoveryPolicy) time.Duration {
	if failures <= 0 {
		return 0
	}
	shift := int(failures - 1)
	if shift > 30 {
		shift = 30
	}
	backoff := policy.BackoffBase * time.Duration(1<<uint(shift))
	if backoff > policy.BackoffMax || backoff < 0 {
		backoff = policy.BackoffMax
	}
	return backoff
}

func shouldEnterSafeMode(consecutiveFailures int32, policy RecoveryPolicy) bool {
	return policy.SafeModeAfter > 0 && consecutiveFailures >= policy.SafeModeAfter
}

// enterRecovery is wired up by the failure-class dispatch in US-24.6.
// Suppressing the unused warning until the call site lands; the function
// is exercised by recovery_policy_test.go in the meantime.
//
//nolint:unused // wired up by US-24.6 follow-up commit
func (r *WorkspaceReconciler) enterRecovery(ctx context.Context, ws *v1.Workspace, class FailureClass) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ws.Status.ConsecutiveFailures++
	ws.Status.LastFailureClass = string(class)
	now := metav1.Now()
	ws.Status.LastFailureAt = &now

	policy := recoveryPolicies[class]

	if shouldEnterSafeMode(ws.Status.ConsecutiveFailures, policy) {
		ws.Status.SafeMode = true
		r.setCondition(ws, v1.WorkspaceConditionType("SafeMode"), "True", "RecoveryExhausted",
			"Entering safe mode after repeated failures")
		logger.Info("Entering safe mode",
			"failures", ws.Status.ConsecutiveFailures, "class", class)
	}

	backoff := calculateBackoff(ws.Status.ConsecutiveFailures, policy)
	if backoff > 0 {
		nextRetry := metav1.NewTime(now.Add(backoff))
		ws.Status.NextRetryAt = &nextRetry
	}

	ws.Status.Phase = v1.WorkspacePhaseCreating
	ws.Status.PodIP = ""
	ws.Status.Endpoint = ""

	logger.Info("Recovery initiated",
		"class", class, "failures", ws.Status.ConsecutiveFailures,
		"backoff", backoff, "safeMode", ws.Status.SafeMode)

	return ctrl.Result{RequeueAfter: backoff}, r.Status().Update(ctx, ws)
}
