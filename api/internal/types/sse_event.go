// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

type WorkspaceSSEEvent struct {
	EventID     uint64      `json:"event_id,omitempty"`
	WorkspaceID string      `json:"workspace_id,omitempty"`
	Type        string      `json:"type"`
	Phase       string      `json:"phase,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	Status      string      `json:"status,omitempty"`
	EventType   string      `json:"event_type,omitempty"`
	Data        interface{} `json:"data,omitempty"`
}
