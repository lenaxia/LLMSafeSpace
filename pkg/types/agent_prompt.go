// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import (
	"encoding/json"
	"time"
)

// PlatformSetting is one row of platform_settings. Used for platform-wide
// mutable configuration like the base system prompt. Key is a stable
// identifier; Value is the raw JSONB payload.
type PlatformSetting struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedBy string          `json:"-"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// PlatformSettingKey identifies a single platform-wide setting.
type PlatformSettingKey string

const (
	SettingSysPromptPlatform PlatformSettingKey = "sys_prompt_platform"
)

// WorkspacePrompt holds the user-level agent customization for a workspace.
// This is only consulted when the org's allow_user_prompt policy is true.
type WorkspacePrompt struct {
	WorkspaceID string    `json:"-"`
	Prompt      string    `json:"prompt"`
	AgentRoleID *string   `json:"agentRoleId,omitempty"`
	UpdatedBy   string    `json:"-"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// EffectivePrompt is the fully resolved system prompt delivered to the pod
// via the bootstrap endpoint and materialized into agentd.AdminPromptPath
// (/sandbox-runtime/admin-prompt.md).
type EffectivePrompt struct {
	PlatformPrompt string `json:"platformPrompt,omitempty"`
	OrgPrompt      string `json:"orgPrompt,omitempty"`
	RolePrompt     string `json:"rolePrompt,omitempty"`
	UserPrompt     string `json:"userPrompt,omitempty"`

	// Resolved is the merged text written to the admin prompt file.
	Resolved string `json:"resolved"`

	// AllowUserPrompt reports whether user customization is enabled for
	// this workspace's org. Delivered so the frontend can show lock state.
	AllowUserPrompt bool `json:"allowUserPrompt"`
}

const maxPromptPerLevel = 10_000

// MaxPromptPerLevel is the character limit for each prompt tier.
func MaxPromptPerLevel() int { return maxPromptPerLevel }
