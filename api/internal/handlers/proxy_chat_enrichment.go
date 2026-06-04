// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// opencode error field allowlist — safe to surface to workspace owners.
var opencodeErrorAllowlist = []string{
	"_tag", "message", "kind", "field", "resource",
	"service", "status", "operation", "ref",
	"providerID", "modelID", "suggestions", "sessionID",
}

// EnrichChatErrorBody adds agentNeedsRefresh hint to error responses
// when the workspace has staged credentials.
func EnrichChatErrorBody(
	body []byte,
	needsRefresh bool,
	since time.Time,
	workspaceID string,
) []byte {
	out := map[string]any{}
	if len(body) > 0 {
		var orig map[string]any
		if json.Unmarshal(body, &orig) == nil {
			for _, k := range opencodeErrorAllowlist {
				if v, ok := orig[k]; ok {
					out[k] = v
				}
			}
		} else {
			text := string(body)
			if len(text) > 1024 {
				text = text[:1024] + "..."
			}
			out["message"] = text
		}
	}
	if needsRefresh {
		out["agentNeedsRefresh"] = true
		out["credentialsPendingSince"] = since.Format(time.RFC3339)
		out["hint"] = fmt.Sprintf(
			"You added or modified llm-provider credentials at %s but have not reloaded "+
				"the agent yet. If this error is related to a provider or model you just changed, "+
				"call POST /api/v1/workspaces/%s/agent/reload to apply the new credentials.",
			since.Format(time.RFC3339), workspaceID,
		)
	}
	result, _ := json.Marshal(out)
	return result
}

// AgentStateChecker is the interface for checking workspace agent state.
type AgentStateChecker interface {
	GetLastCredentialChangedAt(ctx context.Context, workspaceID string) (time.Time, error)
}
