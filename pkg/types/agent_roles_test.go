// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import (
	"encoding/json"
	"testing"
)

func TestUnmarshalRoleConfig_PreservesUnknownKeys(t *testing.T) {
	raw := `{"version":1,"system":"test","futureKey":"futureValue","nested":{"a":1}}`
	cfg, err := UnmarshalRoleConfig([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.System == nil || *cfg.System != "test" {
		t.Errorf("expected system='test', got %v", cfg.System)
	}
	if cfg.Raw["futureKey"] != "futureValue" {
		t.Errorf("expected futureKey in Raw, got %v", cfg.Raw["futureKey"])
	}
	if cfg.Raw["nested"] == nil {
		t.Error("expected nested key in Raw")
	}
}

func TestUnmarshalRoleConfig_DefaultVersion(t *testing.T) {
	raw := `{"system":"test"}`
	cfg, err := UnmarshalRoleConfig([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != RoleConfigVersion {
		t.Errorf("expected version=%d, got %d", RoleConfigVersion, cfg.Version)
	}
}

func TestMarshalRoleConfig_RoundTripsUnknownKeys(t *testing.T) {
	original := `{"version":1,"system":"test","customField":42}`
	cfg, err := UnmarshalRoleConfig([]byte(original))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	marshaled, err := MarshalRoleConfig(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(marshaled, &result); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if result["customField"] == nil {
		t.Error("customField lost during marshal round-trip")
	}
	if result["system"] != "test" {
		t.Errorf("system field corrupted: %v", result["system"])
	}
}

func TestMarshalRoleConfig_NilReturnsDefault(t *testing.T) {
	data, err := MarshalRoleConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	json.Unmarshal(data, &result)
	if result["version"] == nil {
		t.Error("expected version field in nil config output")
	}
}

func TestMergeRoleConfigs_ChildOverridesParent(t *testing.T) {
	parentSys := "parent system"
	childSys := "child system"
	parent := &RoleConfig{
		Version: 1,
		System:  &parentSys,
		Model:   strPtr("gpt-4"),
	}
	child := &RoleConfig{
		Version: 1,
		System:  &childSys,
	}

	merged := MergeRoleConfigs(parent, child)
	if merged.System == nil || *merged.System != "child system" {
		t.Errorf("expected child system to win, got %v", merged.System)
	}
	if merged.Model == nil || *merged.Model != "gpt-4" {
		t.Errorf("expected parent model to inherit, got %v", merged.Model)
	}
}

func TestMergeRoleConfigs_PermissionsConcatenate(t *testing.T) {
	parent := &RoleConfig{
		Version: 1,
		Permissions: []PermissionRule{
			{Action: "edit", Resource: "**", Effect: "allow"},
		},
	}
	child := &RoleConfig{
		Version: 1,
		Permissions: []PermissionRule{
			{Action: "edit", Resource: "*compliance*", Effect: "deny"},
		},
	}

	merged := MergeRoleConfigs(parent, child)
	if len(merged.Permissions) != 2 {
		t.Fatalf("expected 2 permissions, got %d", len(merged.Permissions))
	}
	if merged.Permissions[0].Resource != "**" {
		t.Error("parent permission should come first")
	}
	if merged.Permissions[1].Resource != "*compliance*" {
		t.Error("child permission should be appended")
	}
}

func TestMergeRoleConfigs_RawKeysMerge(t *testing.T) {
	parent := &RoleConfig{
		Version: 1,
		Raw:     map[string]any{"shared": "parent", "parentOnly": true},
	}
	child := &RoleConfig{
		Version: 1,
		Raw:     map[string]any{"shared": "child", "childOnly": 42},
	}

	merged := MergeRoleConfigs(parent, child)
	if merged.Raw["shared"] != "child" {
		t.Error("child Raw key should override parent")
	}
	if merged.Raw["parentOnly"] != true {
		t.Error("parent-only Raw key should pass through")
	}
	if merged.Raw["childOnly"] != 42 {
		t.Error("child-only Raw key should be present")
	}
}

func strPtr(s string) *string { return &s }
