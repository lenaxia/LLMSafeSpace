// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"encoding/json"

	"github.com/lenaxia/llmsafespace/pkg/agent"
)

type OpenCodeAgent struct{}

func (a *OpenCodeAgent) Type() agent.AgentType { return agent.AgentTypeOpenCode }

func (a *OpenCodeAgent) ValidateCredentials(rawConfig []byte) (*agent.CredentialCheckResult, error) {
	if len(rawConfig) == 0 || string(rawConfig) == "{}" {
		return &agent.CredentialCheckResult{
			State:   agent.CredentialStateMissing,
			Agent:   agent.AgentTypeOpenCode,
			Message: "empty config",
		}, nil
	}
	var config map[string]interface{}
	if err := json.Unmarshal(rawConfig, &config); err != nil {
		return &agent.CredentialCheckResult{
			State:   agent.CredentialStateInvalid,
			Agent:   agent.AgentTypeOpenCode,
			Message: "invalid JSON",
		}, nil
	}
	if len(config) == 0 {
		return &agent.CredentialCheckResult{
			State:   agent.CredentialStateMissing,
			Agent:   agent.AgentTypeOpenCode,
			Message: "empty config object",
		}, nil
	}
	return &agent.CredentialCheckResult{
		State: agent.CredentialStatePresent,
		Agent: agent.AgentTypeOpenCode,
	}, nil
}

func (a *OpenCodeAgent) FormatCredentials(rawConfig []byte) ([]byte, error) {
	return rawConfig, nil
}
