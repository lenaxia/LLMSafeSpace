// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package billing

import (
	"testing"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

func TestGetPlanFeatures_Free(t *testing.T) {
	f := GetPlanFeatures(types.PlanFree)
	if f.MaxWorkspaces != 1 {
		t.Errorf("expected free maxWorkspaces 1, got %d", f.MaxWorkspaces)
	}
	if f.SSOEnabled {
		t.Error("free plan should not have SSO")
	}
}

func TestGetPlanFeatures_Team(t *testing.T) {
	f := GetPlanFeatures(types.PlanTeam)
	if f.MaxWorkspaces != 10 {
		t.Errorf("expected team maxWorkspaces 10, got %d", f.MaxWorkspaces)
	}
}

func TestGetPlanFeatures_Business(t *testing.T) {
	f := GetPlanFeatures(types.PlanBusiness)
	if !f.PoliciesEnabled {
		t.Error("business plan should have policies")
	}
	if !f.AuditLogEnabled {
		t.Error("business plan should have audit log")
	}
}

func TestGetPlanFeatures_Enterprise(t *testing.T) {
	f := GetPlanFeatures(types.PlanEnterprise)
	if f.MaxWorkspaces != -1 {
		t.Errorf("expected enterprise unlimited workspaces (-1), got %d", f.MaxWorkspaces)
	}
	if !f.SSOEnabled {
		t.Error("enterprise plan should have SSO")
	}
}

func TestGetPlanFeatures_Unknown_DefaultsToFree(t *testing.T) {
	f := GetPlanFeatures(types.OrgPlan("nonexistent"))
	if f.MaxWorkspaces != 1 {
		t.Errorf("expected unknown plan to default to free, got %d", f.MaxWorkspaces)
	}
}

func TestIsFeatureAllowed(t *testing.T) {
	if !IsFeatureAllowed(types.PlanBusiness, "policies") {
		t.Error("business should allow policies")
	}
	if IsFeatureAllowed(types.PlanFree, "sso") {
		t.Error("free should not allow SSO")
	}
	if IsFeatureAllowed(types.PlanFree, "audit") {
		t.Error("free should not allow audit")
	}
	if !IsFeatureAllowed(types.PlanFree, "custom_credentials") {
		t.Error("free should allow custom credentials")
	}
}

func TestDefaultTrialConfig(t *testing.T) {
	if DefaultTrialConfig.Enabled {
		t.Error("trials should be off by default (D13)")
	}
	if DefaultTrialConfig.DurationDays != 3 {
		t.Errorf("expected 3-day default trial, got %d", DefaultTrialConfig.DurationDays)
	}
}
