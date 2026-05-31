// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
	"sync"
)

type AgentType string

const (
	AgentTypeOpenCode   AgentType = "opencode"
	AgentTypeClaudeCode AgentType = "claude-code"
	AgentTypeCodex      AgentType = "codex"
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
	FormatCredentials(rawConfig []byte) ([]byte, error)
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
