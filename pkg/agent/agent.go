// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
	"sync"
)

type AgentType string

const (
	AgentTypeOpenCode AgentType = "opencode"
)

type CredentialState string

const (
	CredentialStatePresent CredentialState = "Present"
	CredentialStateMissing CredentialState = "Missing"
	CredentialStateInvalid CredentialState = "Invalid"
)

type CredentialCheckResult struct {
	State   CredentialState `json:"state"`
	Agent   AgentType       `json:"agent"`
	Message string          `json:"message,omitempty"`
}

type AgentRuntime interface {
	Type() AgentType
	ValidateCredentials(rawConfig []byte) (*CredentialCheckResult, error)
	FormatProviderConfig(providers []LLMProviderData) ([]byte, error)
}

// LLMProviderData is re-exported from pkg/secrets for use in the interface.
// This avoids a circular import between pkg/agent and pkg/secrets.
type LLMProviderData struct {
	Provider   string           `json:"provider"`
	APIKey     string           `json:"apiKey"`
	BaseURL    string           `json:"baseURL,omitempty"`
	Models     []LLMModelConfig `json:"models,omitempty"`
	Default    string           `json:"default,omitempty"`
	SmallModel string           `json:"smallModel,omitempty"`
}

// LLMModelConfig specifies a model identifier, optional display label, and
// optional context/output token limits.
//
// ContextLimit and OutputLimit MUST be set together (both > 0) to be emitted
// into opencode's agent-config.json — opencode's published JSON Schema
// (https://opencode.ai/config.json) requires both `context` and `output` when
// the `limit` object is present. See pkg/secrets/types.go LLMModelConfig for
// the authoritative documentation.
type LLMModelConfig struct {
	ID           string `json:"id"`
	Label        string `json:"label,omitempty"`
	ContextLimit int    `json:"contextLimit,omitempty"`
	OutputLimit  int    `json:"outputLimit,omitempty"`
}

var (
	registryMu sync.RWMutex
	registry   = map[AgentType]AgentRuntime{}
)

func Get(agentType AgentType) (AgentRuntime, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	a, ok := registry[agentType]
	if !ok {
		return nil, fmt.Errorf("unknown agent type: %s", agentType)
	}
	return a, nil
}

func Register(agentType AgentType, a AgentRuntime) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[agentType] = a
}

func Unregister(agentType AgentType) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, agentType)
}
