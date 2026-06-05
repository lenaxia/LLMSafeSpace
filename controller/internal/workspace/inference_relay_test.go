// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildOpenCodeAuthContent_NoRelay(t *testing.T) {
	r := &WorkspaceReconciler{InferenceRelayURL: ""}
	content := r.buildOpenCodeAuthContent()

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(content), &parsed))

	oc := parsed["opencode"].(map[string]interface{})
	assert.Equal(t, "api", oc["type"])
	assert.Equal(t, "public", oc["key"])
	assert.Nil(t, oc["metadata"]) // no metadata when relay disabled
}

func TestBuildOpenCodeAuthContent_WithRelay(t *testing.T) {
	r := &WorkspaceReconciler{InferenceRelayURL: "https://my-worker.workers.dev"}
	content := r.buildOpenCodeAuthContent()

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(content), &parsed))

	oc := parsed["opencode"].(map[string]interface{})
	assert.Equal(t, "api", oc["type"])
	assert.Equal(t, "public", oc["key"])

	metadata := oc["metadata"].(map[string]interface{})
	assert.Equal(t, "https://my-worker.workers.dev", metadata["baseURL"])
}
