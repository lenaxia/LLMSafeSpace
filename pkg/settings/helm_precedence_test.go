// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStore is a minimal InstanceStore backed by a map for testing the
// helm-precedence logic without a database.
type stubStore struct {
	data map[string]json.RawMessage
}

func (s *stubStore) GetAllInstanceSettings(_ context.Context) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out, nil
}

func (s *stubStore) SetInstanceSetting(_ context.Context, key string, value json.RawMessage) error {
	s.data[key] = value
	return nil
}

func newTestInstanceService() *InstanceService {
	return NewInstanceService(&stubStore{data: map[string]json.RawMessage{}}, nil)
}

// TestSetHelmOverrides_MarksKeysReadOnly verifies that after calling
// SetHelmOverrides, the schema reports those keys as ReadOnly=true so the
// frontend can disable them.
func TestSetHelmOverrides_MarksKeysReadOnly(t *testing.T) {
	svc := newTestInstanceService()
	svc.SetHelmOverrides(map[string]any{
		"instance.name": "MyCorp",
	})

	schema := svc.Schema()
	for _, def := range schema {
		if def.Key == "instance.name" {
			assert.True(t, def.ReadOnly, "helm-managed key must be ReadOnly in schema")
			return
		}
	}
	t.Fatal("instance.name not found in schema")
}

// TestSetHelmOverrides_NonOverriddenKeysStayMutable verifies that keys NOT
// in the overrides map remain editable.
func TestSetHelmOverrides_NonOverriddenKeysStayMutable(t *testing.T) {
	svc := newTestInstanceService()
	svc.SetHelmOverrides(map[string]any{
		"instance.name": "MyCorp",
	})

	schema := svc.Schema()
	for _, def := range schema {
		if def.Key == "auth.registrationEnabled" {
			assert.False(t, def.ReadOnly, "non-helm-managed key must stay mutable")
			return
		}
	}
	t.Fatal("auth.registrationEnabled not found in schema")
}

// TestGetAll_ServesHelmValueForOverriddenKey verifies that GetAll returns
// the helm-provided value (not the DB value) for helm-managed keys.
func TestGetAll_ServesHelmValueForOverriddenKey(t *testing.T) {
	store := &stubStore{data: map[string]json.RawMessage{
		"instance.name": json.RawMessage(`"DBValue"`),
	}}
	svc := NewInstanceService(store, nil)
	svc.SetHelmOverrides(map[string]any{
		"instance.name": "HelmValue",
	})
	require.NoError(t, svc.Start())

	all, err := svc.GetAll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "HelmValue", all["instance.name"],
		"helm value must win over DB value")
}

// TestSet_RejectsReadOnlyKey verifies that writing to a helm-managed key
// returns ErrReadOnly.
func TestSet_RejectsReadOnlyKey(t *testing.T) {
	svc := newTestInstanceService()
	svc.SetHelmOverrides(map[string]any{
		"instance.name": "HelmValue",
	})

	err := svc.Set(context.Background(), "instance.name", "AttemptedOverride")
	assert.ErrorIs(t, err, ErrReadOnly)
}

// TestSet_AllowsNonReadOnlyKey verifies non-managed keys are still writable.
func TestSet_AllowsNonReadOnlyKey(t *testing.T) {
	svc := newTestInstanceService()
	err := svc.Set(context.Background(), "auth.registrationEnabled", false)
	require.NoError(t, err)

	v, err := svc.GetBool(context.Background(), "auth.registrationEnabled")
	require.NoError(t, err)
	assert.False(t, v)
}

// TestSetHelmOverrides_IgnoresUnknownKeys verifies that keys not in the
// schema are silently ignored (defensive — no panic, no error).
func TestSetHelmOverrides_IgnoresUnknownKeys(t *testing.T) {
	svc := newTestInstanceService()
	svc.SetHelmOverrides(map[string]any{
		"email.nonexistent": "value",
	})
	// Should not panic; the unknown key is simply not tracked.
	schema := svc.Schema()
	for _, def := range schema {
		assert.False(t, def.ReadOnly, "no key should be ReadOnly after setting only an unknown key")
	}
}
