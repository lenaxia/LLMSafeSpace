package opencode

import "github.com/lenaxia/llmsafespace/pkg/agent"

func Register() {
	agent.Register(agent.AgentTypeOpenCode, &OpenCodeAgent{})
}
