// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func TestBuildOpenCodeAuthContent_NoRelay(t *testing.T) {
	r := &WorkspaceReconciler{InferenceRelayURL: ""}
	content := r.buildOpenCodeAuthContent()

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(content), &parsed))

	oc := parsed["opencode"].(map[string]interface{})
	assert.Equal(t, "api", oc["type"])
	assert.Equal(t, "public", oc["key"])
	assert.Nil(t, oc["metadata"])
}

func TestBuildOpenCodeAuthContent_WithRelay(t *testing.T) {
	r := &WorkspaceReconciler{InferenceRelayURL: "https://relay.safespaces.dev"}
	content := r.buildOpenCodeAuthContent()

	var parsed map[string]struct {
		Type     string            `json:"type"`
		Key      string            `json:"key"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal([]byte(content), &parsed))

	oc := parsed["opencode"]
	assert.Equal(t, "api", oc.Type)
	assert.Equal(t, "public", oc.Key)
	assert.Equal(t, "https://relay.safespaces.dev", oc.Metadata["baseURL"])
}

func TestBuildOpenCodeAuthContent_SpecialCharsInURL(t *testing.T) {
	// Verify json.Marshal handles URLs with special characters safely
	r := &WorkspaceReconciler{InferenceRelayURL: "https://relay.safespaces.dev/path?key=val&x=1"}
	content := r.buildOpenCodeAuthContent()

	var parsed map[string]struct {
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal([]byte(content), &parsed))
	assert.Equal(t, "https://relay.safespaces.dev/path?key=val&x=1", parsed["opencode"].Metadata["baseURL"])
}

func TestPodBuilder_EnvContainsAuthContent_WithRelay(t *testing.T) {
	r := &WorkspaceReconciler{InferenceRelayURL: "https://relay.safespaces.dev"}
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-test", Namespace: "default"},
		Spec:       v1.WorkspaceSpec{Runtime: "base"},
	}

	// buildOpenCodeAuthContent is called inside buildPod; we can't easily call
	// buildPod without a full K8s client. Instead verify the content directly
	// matches what would be injected.
	content := r.buildOpenCodeAuthContent()

	// Must be valid JSON
	var raw json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(content), &raw))

	// Must contain the relay URL
	assert.Contains(t, content, "relay.safespaces.dev")
	assert.Contains(t, content, `"baseURL"`)
	assert.Contains(t, content, `"public"`)

	_ = ws // used to document what workspace this applies to
}
