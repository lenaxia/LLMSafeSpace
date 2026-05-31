// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_GetUnknown(t *testing.T) {
	_, err := Get(AgentType("unknown"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent type")
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	testType := AgentType("test-agent")
	a := &mockAgent{}
	Register(testType, a)
	defer Unregister(testType)

	got, err := Get(testType)
	require.NoError(t, err)
	assert.Equal(t, a, got)
	assert.Equal(t, testType, got.Type())
}

func TestRegistry_RegisterOverwrite(t *testing.T) {
	testType := AgentType("test-overwrite")
	first := &mockAgent{}
	second := &credentialTrackingAgent{}
	Register(testType, first)
	defer Unregister(testType)

	Register(testType, second)

	got, err := Get(testType)
	require.NoError(t, err)
	_, ok := got.(*credentialTrackingAgent)
	assert.True(t, ok, "should return the overwritten implementation")
}

func TestRegistry_MultipleTypes(t *testing.T) {
	types := []AgentType{"agent-a", "agent-b", "agent-c"}
	for _, at := range types {
		Register(at, &mockAgent{})
		defer Unregister(at)
	}

	for _, at := range types {
		got, err := Get(at)
		require.NoError(t, err)
		assert.NotNil(t, got)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	testType := AgentType("test-unreg")
	Register(testType, &mockAgent{})

	got, err := Get(testType)
	require.NoError(t, err)
	assert.NotNil(t, got)

	Unregister(testType)

	_, err = Get(testType)
	assert.Error(t, err, "should error after unregister")
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	testType := AgentType("test-concurrent")
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			Register(testType, &mockAgent{})
		}
	}()

	for i := 0; i < 100; i++ {
		Get(testType)
	}
	<-done
	Unregister(testType)
}

type mockAgent struct{}

func (m *mockAgent) Type() AgentType { return "test-agent" }
func (m *mockAgent) ValidateCredentials(rawConfig []byte) (*CredentialCheckResult, error) {
	return &CredentialCheckResult{State: CredentialStatePresent, Agent: "test-agent"}, nil
}
func (m *mockAgent) FormatCredentials(rawConfig []byte) ([]byte, error) { return rawConfig, nil }

type credentialTrackingAgent struct{}

func (c *credentialTrackingAgent) Type() AgentType { return "test-overwrite" }
func (c *credentialTrackingAgent) ValidateCredentials(rawConfig []byte) (*CredentialCheckResult, error) {
	return &CredentialCheckResult{State: CredentialStatePresent, Agent: "test-overwrite"}, nil
}
func (c *credentialTrackingAgent) FormatCredentials(rawConfig []byte) ([]byte, error) {
	return rawConfig, nil
}
