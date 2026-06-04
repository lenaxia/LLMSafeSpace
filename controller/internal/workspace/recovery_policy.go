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

func timeUntilNextRetry(ws *v1.Workspace) time.Duration {
	if ws.Status.NextRetryAt == nil {
		return 0
	}
	remaining := time.Until(ws.Status.NextRetryAt.Time)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// maybeResetConsecutiveFailures clears recovery state after the workspace
// has been healthy for the stability window (2 min). If LastStableAt is nil
// and there are outstanding failures, it starts the clock.
func maybeResetConsecutiveFailures(ws *v1.Workspace) {
	if ws.Status.ConsecutiveFailures == 0 {
		return
	}
	if ws.Status.LastStableAt == nil {
		now := metav1.Now()
		ws.Status.LastStableAt = &now
		return
	}
	elapsed := time.Since(ws.Status.LastStableAt.Time)
	if elapsed >= stabilityResetWindow {
		ws.Status.ConsecutiveFailures = 0
		ws.Status.LastFailureClass = ""
		ws.Status.LastFailureAt = nil
		ws.Status.NextRetryAt = nil
		ws.Status.LastStableAt = nil
	}
}

const stabilityResetWindow = 2 * time.Minute

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
