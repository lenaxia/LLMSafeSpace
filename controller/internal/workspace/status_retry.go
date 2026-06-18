// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	"github.com/lenaxia/llmsafespace/controller/internal/metrics"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// updateStatusWithRetry wraps Status().Update in retry.RetryOnConflict
// (US-23.2). On conflict, re-fetches the latest Workspace and re-applies
// the supplied mutation closure, then re-attempts the status update.
// Mirrors the pattern used in api/internal/services/activity/tracker.go
// and api/internal/services/workspace/workspace_service.go.
//
// The mutate closure receives a freshly-fetched Workspace and applies
// the intended status mutation. For deterministic mutations (e.g.,
// "set Phase=Active, set PodIP=X") the closure captures the desired
// values and writes them directly. For non-deterministic mutations
// (e.g., "increment ConsecutiveFailures") the closure must derive the
// new value from the freshly-fetched current value, not from a captured
// local — otherwise a conflict-retry would clobber a concurrent write.
//
// Safe to call after US-23.3 single-writer migration because each
// WorkspaceStatus field has exactly one owner; a conflict-retry only
// re-applies the controller's own fields, never clobbering an API
// service write (which now goes through Spec or annotations).
func (r *WorkspaceReconciler) updateStatusWithRetry(
	ctx context.Context,
	nn types.NamespacedName,
	mutate func(*v1.Workspace),
) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var fresh v1.Workspace
		if err := r.Get(ctx, nn, &fresh); err != nil {
			return err
		}
		mutate(&fresh)
		if err := r.Status().Update(ctx, &fresh); err != nil {
			if apierrors.IsConflict(err) {
				metrics.WorkspaceStatusUpdateConflictsTotal.WithLabelValues("controller_retry").Inc()
			}
			return err
		}
		return nil
	})
}

// clearSuspendRequest acknowledges a Spec.Suspend request by setting it
// back to nil. This MUST be called by the controller after it acts on a
// suspend or resume request, otherwise the stale pointer causes an
// infinite suspend/resume loop (handleSuspended would see &false after
// every controller-initiated suspend and immediately resume).
//
// Uses Update (not Status().Update) because Spec.Suspend is in the spec
// subresource, not status. Retries on conflict.
func (r *WorkspaceReconciler) clearSuspendRequest(ctx context.Context, workspace *v1.Workspace) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var fresh v1.Workspace
		nn := types.NamespacedName{Name: workspace.Name, Namespace: workspace.Namespace}
		if err := r.Get(ctx, nn, &fresh); err != nil {
			return err
		}
		if fresh.Spec.Suspend == nil {
			return nil // already cleared
		}
		fresh.Spec.Suspend = nil
		return r.Update(ctx, &fresh)
	})
}
