// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import "github.com/lenaxia/llmsafespaces/pkg/agent"

func Register() {
	agent.Register(agent.AgentTypeOpenCode, &OpenCodeAgent{})
}
