// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeBindingsDiff_LLMProviderAdded(t *testing.T) {
	existing := []*UserSecret{{ID: "s1", Type: "env-secret"}}
	newSecrets := []*UserSecret{
		{ID: "s1", Type: "env-secret"},
		{ID: "s2", Type: SecretTypeLLMProvider},
	}
	result := computeBindingsDiff(existing, newSecrets)
	assert.True(t, result.LLMProviderAffected)
	assert.Equal(t, []string{string(SecretTypeLLMProvider)}, result.AddedTypes)
	assert.Nil(t, result.RemovedTypes)
}

func TestComputeBindingsDiff_LLMProviderRemoved(t *testing.T) {
	existing := []*UserSecret{
		{ID: "s1", Type: "env-secret"},
		{ID: "s2", Type: SecretTypeLLMProvider},
	}
	newSecrets := []*UserSecret{{ID: "s1", Type: "env-secret"}}
	result := computeBindingsDiff(existing, newSecrets)
	assert.True(t, result.LLMProviderAffected)
	assert.Nil(t, result.AddedTypes)
	assert.Equal(t, []string{string(SecretTypeLLMProvider)}, result.RemovedTypes)
}

func TestComputeBindingsDiff_EnvOnly_NotAffected(t *testing.T) {
	existing := []*UserSecret{{ID: "s1", Type: "env-secret"}}
	newSecrets := []*UserSecret{
		{ID: "s1", Type: "env-secret"},
		{ID: "s2", Type: "env-secret"},
	}
	result := computeBindingsDiff(existing, newSecrets)
	assert.False(t, result.LLMProviderAffected)
	assert.Equal(t, []string{"env-secret"}, result.AddedTypes)
	assert.Nil(t, result.RemovedTypes)
}

func TestComputeBindingsDiff_NilExisting_NoRemovals(t *testing.T) {
	newSecrets := []*UserSecret{
		{ID: "s1", Type: SecretTypeLLMProvider},
		{ID: "s2", Type: "env-secret"},
	}
	result := computeBindingsDiff(nil, newSecrets)
	assert.True(t, result.LLMProviderAffected)
	assert.Nil(t, result.RemovedTypes)
	assert.Len(t, result.AddedTypes, 2)
}

func TestComputeBindingsDiff_BothEmpty(t *testing.T) {
	result := computeBindingsDiff(nil, nil)
	assert.False(t, result.LLMProviderAffected)
	assert.Nil(t, result.AddedTypes)
	assert.Nil(t, result.RemovedTypes)
}

func TestComputeBindingsDiff_NoChange(t *testing.T) {
	secrets := []*UserSecret{
		{ID: "s1", Type: SecretTypeLLMProvider},
		{ID: "s2", Type: "env-secret"},
	}
	result := computeBindingsDiff(secrets, secrets)
	assert.False(t, result.LLMProviderAffected)
	assert.Nil(t, result.AddedTypes)
	assert.Nil(t, result.RemovedTypes)
}

func TestComputeBindingsDiff_AddAndRemove(t *testing.T) {
	existing := []*UserSecret{
		{ID: "s1", Type: SecretTypeLLMProvider},
	}
	newSecrets := []*UserSecret{
		{ID: "s2", Type: SecretTypeLLMProvider},
	}
	result := computeBindingsDiff(existing, newSecrets)
	assert.True(t, result.LLMProviderAffected)
	assert.Equal(t, []string{string(SecretTypeLLMProvider)}, result.AddedTypes)
	assert.Equal(t, []string{string(SecretTypeLLMProvider)}, result.RemovedTypes)
}

func TestSortedKeys_Deterministic(t *testing.T) {
	m := map[string]struct{}{"z": {}, "a": {}, "m": {}}
	result := sortedKeys(m)
	assert.Equal(t, []string{"a", "m", "z"}, result)
	// Call again to verify determinism
	assert.Equal(t, []string{"a", "m", "z"}, sortedKeys(m))
}

func TestSortedKeys_Empty(t *testing.T) {
	assert.Equal(t, 0, len(sortedKeys(map[string]struct{}{})))
	assert.Equal(t, 0, len(sortedKeys(nil)))
}
