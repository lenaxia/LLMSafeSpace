// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import "sort"

// BindingsMutationResult describes what changed in a SetBindings or AddBindings call.
// Returned as a value (not pointer) so callers never need a nil check.
type BindingsMutationResult struct {
	// LLMProviderAffected is true if any llm-provider secret was added or removed,
	// OR if the diff could not be computed (conservative fallback).
	LLMProviderAffected bool
	AddedTypes          []string
	RemovedTypes        []string
}

func computeBindingsDiff(existing, newSecrets []*UserSecret) BindingsMutationResult {
	existingByID := make(map[string]*UserSecret, len(existing))
	for _, s := range existing {
		existingByID[s.ID] = s
	}
	newByID := make(map[string]*UserSecret, len(newSecrets))
	for _, s := range newSecrets {
		newByID[s.ID] = s
	}

	addedTypes := map[string]struct{}{}
	for _, s := range newSecrets {
		if _, wasPresent := existingByID[s.ID]; !wasPresent {
			addedTypes[string(s.Type)] = struct{}{}
		}
	}

	removedTypes := map[string]struct{}{}
	for _, s := range existing {
		if _, stillPresent := newByID[s.ID]; !stillPresent {
			removedTypes[string(s.Type)] = struct{}{}
		}
	}

	_, llmAdded := addedTypes[string(SecretTypeLLMProvider)]
	_, llmRemoved := removedTypes[string(SecretTypeLLMProvider)]

	return BindingsMutationResult{
		LLMProviderAffected: llmAdded || llmRemoved,
		AddedTypes:          sortedKeys(addedTypes),
		RemovedTypes:        sortedKeys(removedTypes),
	}
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
