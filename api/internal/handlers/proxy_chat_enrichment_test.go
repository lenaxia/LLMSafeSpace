// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnrichChatErrorBody_AgentNeedsRefresh_AddsHint(t *testing.T) {
	body := []byte(`{"_tag":"ModelNotFoundError","message":"model not found","modelID":"gpt-4"}`)
	since := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

	result := EnrichChatErrorBody(body, true, since, "ws-123")

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(result, &out))
	assert.Equal(t, true, out["agentNeedsRefresh"])
	assert.Contains(t, out["hint"], "reload")
	assert.Contains(t, out["hint"], "ws-123")
	assert.Equal(t, "ModelNotFoundError", out["_tag"])
	assert.Equal(t, "model not found", out["message"])
	assert.Equal(t, "gpt-4", out["modelID"])
}

func TestEnrichChatErrorBody_FlagFalse_NoHint(t *testing.T) {
	body := []byte(`{"_tag":"SessionBusyError","message":"busy","sessionID":"s1"}`)

	result := EnrichChatErrorBody(body, false, time.Time{}, "ws-123")

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(result, &out))
	assert.Nil(t, out["agentNeedsRefresh"])
	assert.Nil(t, out["hint"])
	assert.Equal(t, "SessionBusyError", out["_tag"])
	assert.Equal(t, "s1", out["sessionID"])
}

func TestEnrichChatErrorBody_AllowlistBlocksUnknownFields(t *testing.T) {
	body := []byte(`{"_tag":"X","message":"msg","internalSecret":"leaked","stackTrace":"..."}`)

	result := EnrichChatErrorBody(body, false, time.Time{}, "ws-1")

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(result, &out))
	assert.Equal(t, "X", out["_tag"])
	assert.Equal(t, "msg", out["message"])
	assert.Nil(t, out["internalSecret"])
	assert.Nil(t, out["stackTrace"])
}

func TestEnrichChatErrorBody_NonJSON_WrappedSafely(t *testing.T) {
	body := []byte("plain text error from upstream proxy")

	result := EnrichChatErrorBody(body, true, time.Now(), "ws-1")

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(result, &out))
	assert.Equal(t, "plain text error from upstream proxy", out["message"])
	assert.Equal(t, true, out["agentNeedsRefresh"])
}

func TestEnrichChatErrorBody_EmptyBody(t *testing.T) {
	result := EnrichChatErrorBody(nil, true, time.Now(), "ws-1")

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(result, &out))
	assert.Equal(t, true, out["agentNeedsRefresh"])
}

func TestEnrichChatErrorBody_ModelNotFound_StructuredFieldsPreserved(t *testing.T) {
	body := []byte(`{
		"_tag":"ModelNotFoundError",
		"message":"model not found",
		"providerID":"openai",
		"modelID":"gpt-5",
		"suggestions":["gpt-4","gpt-4o"]
	}`)

	result := EnrichChatErrorBody(body, false, time.Time{}, "ws-1")

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(result, &out))
	assert.Equal(t, "openai", out["providerID"])
	assert.Equal(t, "gpt-5", out["modelID"])
	suggestions, ok := out["suggestions"].([]interface{})
	require.True(t, ok)
	assert.Len(t, suggestions, 2)
}
