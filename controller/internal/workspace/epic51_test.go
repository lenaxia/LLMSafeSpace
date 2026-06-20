// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

// Epic 51 — Tenant Isolation tests.
//
// S51.1: gVisor RuntimeClass — verify the controller sets RuntimeClassName
//        from DefaultRuntimeClass and that per-workspace spec.runtimeClass
//        overrides take precedence.
// S51.3: Tenant pod label — verify llmsafespaces.dev/tenant is set to
//        OrgID when present, else UserID.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// =============================================================================
// S51.3 — Tenant pod label
// =============================================================================

func TestS51_3_TenantLabel_UserOnly(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	ws.Spec.Owner = v1.WorkspaceOwner{UserID: "user-abc"}
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	tenant, ok := pod.Labels[LabelTenant]
	require.True(t, ok, "pod must have %s label", LabelTenant)
	require.Equal(t, "user-abc", tenant,
		"tenant label must be user_id when no org_id is set")
}

func TestS51_3_TenantLabel_OrgMember(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	ws.Spec.Owner = v1.WorkspaceOwner{UserID: "user-abc", OrgID: "org-xyz"}
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	tenant, ok := pod.Labels[LabelTenant]
	require.True(t, ok, "pod must have %s label", LabelTenant)
	require.Equal(t, "org-xyz", tenant,
		"tenant label must be org_id when set (Design 0031 D4 — org members are org-attributed)")
}

func TestS51_3_TenantLabel_EmptyOwner(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	ws.Spec.Owner = v1.WorkspaceOwner{}
	r := reconcilerFor(t)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	tenant, ok := pod.Labels[LabelTenant]
	require.True(t, ok, "pod must always have %s label, even if owner is empty", LabelTenant)
	require.Equal(t, "unspecified", tenant,
		"tenant label must be 'unspecified' when both UserID and OrgID are unset (sanitizeLabelValue)")
}

func TestS51_3_TenantIDResolution(t *testing.T) {
	require.Equal(t, "org-1", tenantID(v1.WorkspaceOwner{UserID: "user-1", OrgID: "org-1"}),
		"org_id takes precedence")
	require.Equal(t, "user-1", tenantID(v1.WorkspaceOwner{UserID: "user-1"}),
		"user_id when no org_id")
	require.Equal(t, "", tenantID(v1.WorkspaceOwner{}),
		"empty when neither set")
}

// =============================================================================
// S51.1 — gVisor RuntimeClass
// =============================================================================

func TestS51_1_DefaultRuntimeClassApplied(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)
	r.DefaultRuntimeClass = "gvisor"

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	require.NotNil(t, pod.Spec.RuntimeClassName,
		"RuntimeClassName must be set when DefaultRuntimeClass is configured")
	require.Equal(t, "gvisor", *pod.Spec.RuntimeClassName,
		"RuntimeClassName must match DefaultRuntimeClass")
}

func TestS51_1_NoDefaultRuntimeClass(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)
	r.DefaultRuntimeClass = ""

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	require.Nil(t, pod.Spec.RuntimeClassName,
		"RuntimeClassName must be nil when DefaultRuntimeClass is empty (dev/single-tenant default)")
}

func TestS51_1_PerWorkspaceOptOut(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)
	r.DefaultRuntimeClass = "gvisor"

	runc := "runc"
	ws.Spec.RuntimeClass = &runc

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	require.NotNil(t, pod.Spec.RuntimeClassName,
		"RuntimeClassName must be set when spec.runtimeClass overrides")
	require.Equal(t, "runc", *pod.Spec.RuntimeClassName,
		"spec.runtimeClass must override DefaultRuntimeClass")
}

func TestS51_1_PerWorkspaceOptOutEmpty(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)
	r.DefaultRuntimeClass = "gvisor"

	empty := ""
	ws.Spec.RuntimeClass = &empty

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	require.Nil(t, pod.Spec.RuntimeClassName,
		"spec.runtimeClass='' must clear RuntimeClassName (explicit runc)")
}

// =============================================================================
// S51.4 — Hardening survives under gVisor RuntimeClass
// =============================================================================

func TestS51_4_HardeningSurvivesUnderGVisor(t *testing.T) {
	ws := newWorkspaceForSecurity(t)
	r := reconcilerFor(t)
	r.DefaultRuntimeClass = "gvisor"

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	require.NotNil(t, pod.Spec.RuntimeClassName,
		"RuntimeClassName must be gvisor")
	require.Equal(t, "gvisor", *pod.Spec.RuntimeClassName)

	require.NotNil(t, pod.Spec.AutomountServiceAccountToken,
		"AutomountServiceAccountToken must still be explicitly false under gVisor")
	require.False(t, *pod.Spec.AutomountServiceAccountToken)

	require.NotNil(t, pod.Spec.EnableServiceLinks,
		"EnableServiceLinks must still be false under gVisor")
	require.False(t, *pod.Spec.EnableServiceLinks)

	require.NotNil(t, pod.Spec.SecurityContext,
		"SecurityContext must still be set under gVisor")
	require.NotNil(t, pod.Spec.SecurityContext.SeccompProfile,
		"SeccompProfile must still be RuntimeDefault under gVisor")
}
