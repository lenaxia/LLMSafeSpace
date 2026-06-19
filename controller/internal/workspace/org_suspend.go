// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// transitionActiveToSuspending is the shared Active→Suspending transition used
// by both the Spec.Suspend path (API-requested suspend) and the org-suspension
// path (D20). It records the phase-transition metric, flips Status.Phase, and
// commits it. Callers are responsible for any follow-up (clearing Spec.Suspend,
// requeueing) and for emitting their own log line describing the reason.
func (r *WorkspaceReconciler) transitionActiveToSuspending(ctx context.Context, workspace *v1.Workspace) error {
	workspacePhaseTransitions.WithLabelValues(string(v1.WorkspacePhaseActive), string(v1.WorkspacePhaseSuspending)).Inc()
	workspace.Status.Phase = v1.WorkspacePhaseSuspending
	if err := r.Status().Update(ctx, workspace); err != nil {
		recordStatusUpdateConflictOnError("handleActive_suspend", err)
		return err
	}
	return nil
}

// applyOrgSuspension implements the D20 org-level suspension check for an
// Active workspace. When the workspace belongs to an org and the OrgStatusClient
// reports the org as suspended, it transitions the workspace to Suspending and
// returns (transitioned=true). On any other status, or when the lookup fails
// (fail-open), it returns (transitioned=false) so handleActive continues its
// normal Active-phase logic.
//
// The controller never auto-resumes on org reactivation (D20): members/admins
// must manually resume each workspace. This method therefore only ever
// suspends.
func (r *WorkspaceReconciler) applyOrgSuspension(ctx context.Context, workspace *v1.Workspace) (bool, error) {
	orgID := workspace.Spec.Owner.OrgID
	if orgID == "" || r.OrgStatusClient == nil {
		return false, nil
	}

	status, ok := r.OrgStatusClient.GetOrgStatus(ctx, orgID)
	if !ok {
		// Fail open: could not determine org status — leave the workspace
		// running. The cached client already logged the failure.
		return false, nil
	}
	if status != orgStatusSuspended {
		return false, nil
	}

	logger := log.FromContext(ctx)
	logger.Info("Org is suspended; transitioning workspace to Suspending (D20)", "orgID", orgID)
	if err := r.transitionActiveToSuspending(ctx, workspace); err != nil {
		return false, err
	}
	return true, nil
}
