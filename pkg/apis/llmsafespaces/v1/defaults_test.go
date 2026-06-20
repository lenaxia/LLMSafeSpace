// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestSetDefaults_WorkspaceSpec verifies every +kubebuilder:default annotation
// on WorkspaceSpec is replicated by the Go defaulter (M5-b). This is the fast
// unit-test seam that catches the class of bug from PR #231 (default:false
// incident): fake-client doesn't apply kubebuilder defaults, so tests that
// create a bare Workspace see zero values where production sees defaults.
//
// The dangerous cases are defaults where the zero value differs from the
// default value — e.g. AutoSuspend.Enabled defaults to true but zero is false.
func TestSetDefaults_WorkspaceSpec(t *testing.T) {
	tests := []struct {
		name      string
		input     WorkspaceSpec
		field     string
		getValue  func(WorkspaceSpec) any
		wantZero  any // value before defaults
		wantAfter any // value after defaults
	}{
		{
			name:      "Architecture defaults to amd64",
			input:     WorkspaceSpec{},
			field:     "Architecture",
			getValue:  func(s WorkspaceSpec) any { return s.Architecture },
			wantZero:  "",
			wantAfter: "amd64",
		},
		{
			name:      "SecurityLevel defaults to standard",
			input:     WorkspaceSpec{},
			field:     "SecurityLevel",
			getValue:  func(s WorkspaceSpec) any { return s.SecurityLevel },
			wantZero:  "",
			wantAfter: "standard",
		},
		{
			name:      "MaxActiveSessions defaults to 5",
			input:     WorkspaceSpec{},
			field:     "MaxActiveSessions",
			getValue:  func(s WorkspaceSpec) any { return s.MaxActiveSessions },
			wantZero:  int32(0),
			wantAfter: int32(5),
		},
		{
			name:      "Storage.AccessMode defaults to ReadWriteOnce",
			input:     WorkspaceSpec{Storage: WorkspaceStorageConfig{}},
			field:     "Storage.AccessMode",
			getValue:  func(s WorkspaceSpec) any { return s.Storage.AccessMode },
			wantZero:  "",
			wantAfter: "ReadWriteOnce",
		},
		{
			name:      "AutoSuspend.Enabled defaults to true (PR 231 class — zero is false!)",
			input:     WorkspaceSpec{AutoSuspend: &WorkspaceAutoSuspend{}},
			field:     "AutoSuspend.Enabled",
			getValue:  func(s WorkspaceSpec) any { return s.AutoSuspend.Enabled },
			wantZero:  false,
			wantAfter: true,
		},
		{
			name:      "AutoSuspend.IdleTimeoutSeconds defaults to 86400",
			input:     WorkspaceSpec{AutoSuspend: &WorkspaceAutoSuspend{}},
			field:     "AutoSuspend.IdleTimeoutSeconds",
			getValue:  func(s WorkspaceSpec) any { return s.AutoSuspend.IdleTimeoutSeconds },
			wantZero:  int64(0),
			wantAfter: int64(86400),
		},
		{
			name:      "Resources.CPU defaults to 500m",
			input:     WorkspaceSpec{Resources: &ResourceRequirements{}},
			field:     "Resources.CPU",
			getValue:  func(s WorkspaceSpec) any { return s.Resources.CPU },
			wantZero:  "",
			wantAfter: "500m",
		},
		{
			name:      "Resources.Memory defaults to 512Mi",
			input:     WorkspaceSpec{Resources: &ResourceRequirements{}},
			field:     "Resources.Memory",
			getValue:  func(s WorkspaceSpec) any { return s.Resources.Memory },
			wantZero:  "",
			wantAfter: "512Mi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantZero, tt.getValue(tt.input), "zero value mismatch before defaults")
			SetDefaults_WorkspaceSpec(&tt.input)
			assert.Equal(t, tt.wantAfter, tt.getValue(tt.input), "default not applied for %s", tt.field)
		})
	}
}

// TestSetDefaults_WorkspaceSpec_PreservesExisting confirms the defaulter is
// idempotent and does NOT clobber values the caller explicitly set.
func TestSetDefaults_WorkspaceSpec_PreservesExisting(t *testing.T) {
	ws := WorkspaceSpec{
		Architecture:      "arm64",
		SecurityLevel:     "high",
		MaxActiveSessions: 10,
		Storage:           WorkspaceStorageConfig{AccessMode: "ReadWriteMany", Size: "20Gi"},
		AutoSuspend:       &WorkspaceAutoSuspend{Enabled: false, IdleTimeoutSeconds: 3600},
		Resources:         &ResourceRequirements{CPU: "2000m", Memory: "4Gi"},
	}

	SetDefaults_WorkspaceSpec(&ws)

	assert.Equal(t, "arm64", ws.Architecture, "explicitly set Architecture must be preserved")
	assert.Equal(t, "high", ws.SecurityLevel, "explicitly set SecurityLevel must be preserved")
	assert.Equal(t, int32(10), ws.MaxActiveSessions, "explicitly set MaxActiveSessions must be preserved")
	assert.Equal(t, "ReadWriteMany", ws.Storage.AccessMode, "explicitly set AccessMode must be preserved")
	assert.Equal(t, "20Gi", ws.Storage.Size, "explicitly set Size must be preserved")
	assert.False(t, ws.AutoSuspend.Enabled, "explicitly set Enabled=false must be preserved")
	assert.Equal(t, int64(3600), ws.AutoSuspend.IdleTimeoutSeconds, "explicitly set IdleTimeoutSeconds must be preserved")
	assert.Equal(t, "2000m", ws.Resources.CPU, "explicitly set CPU must be preserved")
	assert.Equal(t, "4Gi", ws.Resources.Memory, "explicitly set Memory must be preserved")
}

// TestSetDefaults_WorkspaceSpec_AutoSuspendNil confirms that a nil AutoSuspend
// pointer is initialized with defaults (not just left nil).
func TestSetDefaults_WorkspaceSpec_AutoSuspendNil(t *testing.T) {
	ws := WorkspaceSpec{}
	SetDefaults_WorkspaceSpec(&ws)
	require.NotNil(t, ws.AutoSuspend, "AutoSuspend must be initialized when nil")
	assert.True(t, ws.AutoSuspend.Enabled)
	assert.Equal(t, int64(86400), ws.AutoSuspend.IdleTimeoutSeconds)
}

// TestSetDefaults_WorkspaceSpec_ResourcesNil confirms that a nil Resources
// pointer is initialized with defaults.
func TestSetDefaults_WorkspaceSpec_ResourcesNil(t *testing.T) {
	ws := WorkspaceSpec{}
	SetDefaults_WorkspaceSpec(&ws)
	require.NotNil(t, ws.Resources, "Resources must be initialized when nil")
	assert.Equal(t, "500m", ws.Resources.CPU)
	assert.Equal(t, "512Mi", ws.Resources.Memory)
}

// TestSetDefaults_InferenceRelaySpec covers the InferenceRelay CRD defaults.
func TestSetDefaults_InferenceRelaySpec(t *testing.T) {
	tests := []struct {
		name      string
		input     InferenceRelaySpec
		getValue  func(InferenceRelaySpec) any
		wantAfter any
	}{
		{"UpstreamURL", InferenceRelaySpec{}, func(s InferenceRelaySpec) any { return s.UpstreamURL }, "https://opencode.ai/zen/v1"},
		{"HealthCheck.UnhealthyThreshold", InferenceRelaySpec{}, func(s InferenceRelaySpec) any { return s.HealthCheck.UnhealthyThreshold }, 3},
		{"Fallback.Enabled", InferenceRelaySpec{}, func(s InferenceRelaySpec) any { return s.Fallback.Enabled }, true},
		{"Rotation.Enabled", InferenceRelaySpec{}, func(s InferenceRelaySpec) any { return s.Rotation.Enabled }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetDefaults_InferenceRelaySpec(&tt.input)
			assert.Equal(t, tt.wantAfter, tt.getValue(tt.input))
		})
	}
}

// TestSetDefaults_InferenceRelaySpec_Durations verifies the metav1.Duration
// defaults are applied correctly.
func TestSetDefaults_InferenceRelaySpec_Durations(t *testing.T) {
	spec := InferenceRelaySpec{}
	SetDefaults_InferenceRelaySpec(&spec)

	assert.Equal(t, "15s", spec.HealthCheck.Interval.Duration.String())
	assert.Equal(t, "5s", spec.HealthCheck.Timeout.Duration.String())
	assert.Equal(t, "15m0s", spec.HealthCheck.ReplacementTimeout.Duration.String())
	assert.Equal(t, "5m0s", spec.Rotation.DetectionWindow.Duration.String())
	assert.Equal(t, "30m0s", spec.Rotation.Cooldown.Duration.String())
}

// TestSetDefaults_OnWorkspace is a convenience wrapper that applies spec
// defaults via the full Workspace object (the form most callers use).
func TestSetDefaults_OnWorkspace(t *testing.T) {
	ws := &Workspace{
		Spec: WorkspaceSpec{
			Owner:   WorkspaceOwner{UserID: "u1"},
			Runtime: "python:3.11",
			Storage: WorkspaceStorageConfig{Size: "5Gi"},
		},
	}
	SetDefaults_Workspace(ws)

	assert.Equal(t, "amd64", ws.Spec.Architecture)
	assert.Equal(t, "standard", ws.Spec.SecurityLevel)
	assert.Equal(t, int32(5), ws.Spec.MaxActiveSessions)
	assert.Equal(t, "ReadWriteOnce", ws.Spec.Storage.AccessMode)
	require.NotNil(t, ws.Spec.AutoSuspend)
	assert.True(t, ws.Spec.AutoSuspend.Enabled)
}

// helpers for test brevity — keep unused import warnings at bay
var _ = metav1.Duration{}
