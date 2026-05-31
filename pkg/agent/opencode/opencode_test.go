// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/pkg/agent"
)

func TestOpenCodeAgent_Type(t *testing.T) {
	a := &OpenCodeAgent{}
	assert.Equal(t, agent.AgentTypeOpenCode, a.Type())
}

func TestOpenCodeAgent_ValidateCredentials_Present(t *testing.T) {
	a := &OpenCodeAgent{}
	result, err := a.ValidateCredentials([]byte(`{"apiKey":"sk-test-123"}`))
	require.NoError(t, err)
	assert.Equal(t, agent.CredentialStatePresent, result.State)
	assert.Equal(t, agent.AgentTypeOpenCode, result.Agent)
}

func TestOpenCodeAgent_ValidateCredentials_EmptyBytes(t *testing.T) {
	a := &OpenCodeAgent{}
	result, err := a.ValidateCredentials([]byte{})
	require.NoError(t, err)
	assert.Equal(t, agent.CredentialStateMissing, result.State)
	assert.Equal(t, "empty config", result.Message)
}

func TestOpenCodeAgent_ValidateCredentials_EmptyJSON(t *testing.T) {
	a := &OpenCodeAgent{}
	result, err := a.ValidateCredentials([]byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, agent.CredentialStateMissing, result.State)
	assert.Equal(t, "empty config", result.Message)
}

func TestOpenCodeAgent_ValidateCredentials_InvalidJSON(t *testing.T) {
	a := &OpenCodeAgent{}
	result, err := a.ValidateCredentials([]byte(`not json`))
	require.NoError(t, err)
	assert.Equal(t, agent.CredentialStateInvalid, result.State)
	assert.Equal(t, "invalid JSON", result.Message)
}

func TestOpenCodeAgent_ValidateCredentials_NilInput(t *testing.T) {
	a := &OpenCodeAgent{}
	result, err := a.ValidateCredentials(nil)
	require.NoError(t, err)
	assert.Equal(t, agent.CredentialStateMissing, result.State)
}

func TestOpenCodeAgent_ValidateCredentials_EmptyObjectAfterUnmarshal(t *testing.T) {
	a := &OpenCodeAgent{}
	result, err := a.ValidateCredentials([]byte(`{"":null}`))
	require.NoError(t, err)
	assert.Equal(t, agent.CredentialStatePresent, result.State)
}

func TestOpenCodeAgent_FormatCredentials(t *testing.T) {
	a := &OpenCodeAgent{}
	input := []byte(`{"apiKey":"test"}`)
	output, err := a.FormatCredentials(input)
	require.NoError(t, err)
	assert.Equal(t, input, output)
}
