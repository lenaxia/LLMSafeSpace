// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// clearSuspendRequest acknowledges a Spec.Suspend request by setting it
// back to nil. This MUST be called by the controller after it acts on a
// suspend or resume request, otherwise the stale pointer causes an
// infinite suspend/resume loop (handleSuspended would see &false after
// every controller-initiated suspend and immediately resume).
//
// Uses Update (not Status().Update) because Spec.Suspend is in the spec
// subresource, not status. Retries on conflict. Fetches its own fresh
// copy so the caller's local workspace pointer remains usable for
// subsequent reads (though its resourceVersion will be stale — callers
// that need to write again must re-fetch).
//
// IMPORTANT ordering: clearSuspendRequest must be called AFTER
// Status().Update commits the phase transition, not before. Calling it
// first bumps the resourceVersion via the spec Update, causing the
// subsequent Status().Update to 409 on the stale local RV and
// permanently lose the request.
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
