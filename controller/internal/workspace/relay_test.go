// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func TestBuildRelayURL(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-abc123"},
	}

	tests := []struct {
		name          string
		apiServiceURL string
		want          string
	}{
		{"http converts to ws", "http://llmsafespace-api.default.svc:8080",
			"ws://llmsafespace-api.default.svc:8080/api/v1/workspaces/ws-abc123/relay?role=agentd"},
		{"https converts to wss", "https://api.example.com",
			"wss://api.example.com/api/v1/workspaces/ws-abc123/relay?role=agentd"},
		{"empty returns empty", "", ""},
		{"no scheme passthrough", "llmsafespace-api:8080",
			"llmsafespace-api:8080/api/v1/workspaces/ws-abc123/relay?role=agentd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &WorkspaceReconciler{APIServiceURL: tt.apiServiceURL}
			got := r.buildRelayURL(ws)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildAgentdEnv_RelayDisabled(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-test"},
	}
	r := &WorkspaceReconciler{APIServiceURL: ""}

	env := r.buildAgentdEnv(ws)

	// Should have base env vars but NOT relay vars
	names := envNames(env)
	assert.Contains(t, names, "WORKSPACE_ID")
	assert.Contains(t, names, "AGENTD_ADMIN_TOKEN")
	assert.NotContains(t, names, "LLMSAFESPACE_RELAY_URL")
	assert.NotContains(t, names, "LLMSAFESPACE_RELAY_TOKEN")
}

func TestBuildAgentdEnv_RelayEnabled(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-test"},
	}
	r := &WorkspaceReconciler{APIServiceURL: "http://api.svc:8080"}

	env := r.buildAgentdEnv(ws)

	names := envNames(env)
	assert.Contains(t, names, "LLMSAFESPACE_RELAY_URL")
	assert.Contains(t, names, "LLMSAFESPACE_RELAY_TOKEN")

	// Verify relay URL value
	for _, e := range env {
		if e.Name == "LLMSAFESPACE_RELAY_URL" {
			require.Equal(t, "ws://api.svc:8080/api/v1/workspaces/ws-test/relay?role=agentd", e.Value)
		}
		if e.Name == "LLMSAFESPACE_RELAY_TOKEN" {
			require.NotNil(t, e.ValueFrom)
			require.Equal(t, "workspace-pw-ws-test", e.ValueFrom.SecretKeyRef.Name)
			require.Equal(t, "password", e.ValueFrom.SecretKeyRef.Key)
		}
	}
}

func envNames(env []corev1.EnvVar) []string {
	names := make([]string, len(env))
	for i, e := range env {
		names[i] = e.Name
	}
	return names
}
