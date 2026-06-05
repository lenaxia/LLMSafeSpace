// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"encoding/json"
	"fmt"
)

// InjectedSecret is a single secret entry in the secrets.json file
// that the init container reads to materialize secrets.
type InjectedSecret struct {
	Type      SecretType      `json:"type"`
	Name      string          `json:"name"`
	Metadata  json.RawMessage `json:"metadata"`
	Plaintext string          `json:"plaintext"`
}

// PrepareSecretsForInjection decrypts all secrets bound to a workspace
// and returns the JSON payload for the ephemeral K8s Secret.
//
// When deriveAdminKey is wired (US-30.5+), uses the new multi-source path
// that queries workspace_credential_bindings and merges by provider priority.
// Otherwise falls back to the legacy path (user_secrets only).
func (s *SecretService) PrepareSecretsForInjection(ctx context.Context, userID, sessionID, workspaceID string) ([]byte, error) {
	if s.deriveAdminKey == nil {
		return s.prepareSecretsLegacy(ctx, userID, sessionID, workspaceID)
	}

	if err := s.verifyWorkspaceOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}

	// Cast store to CredentialStore. PgSecretStore satisfies both interfaces.
	credStore, ok := s.store.(CredentialStore)
	if !ok {
		return s.prepareSecretsLegacy(ctx, userID, sessionID, workspaceID)
	}

	// Load all bound credentials, ordered by priority.
	bindings, err := credStore.GetWorkspaceCredentials(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("get workspace credentials: %w", err)
	}

	// Derive server KEK once if any admin credentials are present.
	var serverKEK []byte
	for _, b := range bindings {
		if b.OwnerType == "admin" {
			serverKEK = s.deriveAdminKey("provider-credentials")
			break
		}
	}

	// Decrypt and deduplicate by provider (first wins per priority order).
	seen := make(map[string]bool)
	var providerData []LLMProviderData
	for _, b := range bindings {
		if seen[b.Provider] {
			continue
		}
		pd, err := s.decryptBinding(ctx, b, sessionID, serverKEK)
		if err != nil {
			continue // skip failed decryption, don't set seen
		}
		if len(b.ModelAllowlist) > 0 {
			allowed := make(map[string]bool, len(b.ModelAllowlist))
			for _, id := range b.ModelAllowlist {
				allowed[id] = true
			}
			var filtered []LLMModelConfig
			for _, m := range pd.Models {
				if allowed[m.ID] {
					filtered = append(filtered, m)
				}
			}
			if len(filtered) == 0 {
				filtered = make([]LLMModelConfig, 0, len(b.ModelAllowlist))
				for _, id := range b.ModelAllowlist {
					filtered = append(filtered, LLMModelConfig{ID: id})
				}
			}
			pd.Models = filtered
		}
		seen[b.Provider] = true
		providerData = append(providerData, pd)
	}

	// Non-LLM secrets from user_secrets (unchanged path).
	nonLLM, err := s.buildNonLLMSecrets(ctx, userID, sessionID, workspaceID)
	if err != nil {
		return nil, err
	}

	return buildSecretsJSON(providerData, nonLLM)
}

func (s *SecretService) decryptBinding(ctx context.Context, b CredentialBinding, sessionID string, serverKEK []byte) (LLMProviderData, error) {
	var key []byte
	switch b.OwnerType {
	case "user":
		dek, err := s.keys.GetDEK(ctx, sessionID)
		if err != nil {
			return LLMProviderData{}, fmt.Errorf("get user DEK: %w", err)
		}
		key = dek
	case "admin":
		if serverKEK == nil {
			return LLMProviderData{}, fmt.Errorf("server KEK unavailable")
		}
		key = serverKEK
	default:
		return LLMProviderData{}, fmt.Errorf("unsupported owner_type %q", b.OwnerType)
	}
	plaintext, err := DecryptSecret(key, b.Ciphertext)
	if err != nil {
		return LLMProviderData{}, err
	}
	var pd LLMProviderData
	if err := json.Unmarshal(plaintext, &pd); err != nil {
		return LLMProviderData{}, fmt.Errorf("unmarshal LLMProviderData: %w", err)
	}
	return pd, nil
}

func (s *SecretService) buildNonLLMSecrets(ctx context.Context, userID, sessionID, workspaceID string) ([]InjectedSecret, error) {
	bound, err := s.store.GetBindings(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var relevant []*UserSecret
	for _, secret := range bound {
		if secret.UserID == userID && secret.Type != SecretTypeLLMProvider {
			relevant = append(relevant, secret)
		}
	}
	if len(relevant) == 0 {
		return nil, nil
	}
	dek, err := s.keys.GetDEK(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get DEK for non-LLM secrets: %w", err)
	}
	var out []InjectedSecret
	for _, secret := range relevant {
		plaintext, err := DecryptSecret(dek, secret.Ciphertext)
		if err != nil {
			continue
		}
		out = append(out, InjectedSecret{
			Type:      secret.Type,
			Name:      secret.Name,
			Metadata:  secret.Metadata,
			Plaintext: string(plaintext),
		})
	}
	return out, nil
}

func buildSecretsJSON(providerData []LLMProviderData, nonLLM []InjectedSecret) ([]byte, error) {
	out := make([]InjectedSecret, 0, len(providerData)+len(nonLLM))
	for _, pd := range providerData {
		plaintext, err := json.Marshal(pd) //nolint:gosec // marshaling for secrets.json injection, not API response
		if err != nil {
			return nil, err
		}
		out = append(out, InjectedSecret{
			Type:      SecretTypeLLMProvider,
			Name:      pd.Provider,
			Plaintext: string(plaintext),
		})
	}
	out = append(out, nonLLM...)
	return json.Marshal(out)
}

// prepareSecretsLegacy is the original PrepareSecretsForInjection implementation.
// Used when deriveAdminKey is not wired (pre-US-30.5 deployments).
func (s *SecretService) prepareSecretsLegacy(ctx context.Context, userID, sessionID, workspaceID string) ([]byte, error) {
	if err := s.verifyWorkspaceOwner(ctx, userID, workspaceID); err != nil {
		return nil, err
	}
	// Get bound secrets
	boundSecrets, err := s.store.GetBindings(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("get bindings: %w", err)
	}

	if len(boundSecrets) == 0 {
		return json.Marshal([]InjectedSecret{})
	}

	dek, err := s.keys.GetDEK(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("encryption unavailable: %w", err)
	}

	injected := make([]InjectedSecret, 0, len(boundSecrets))
	for _, secret := range boundSecrets {
		// Only decrypt secrets owned by this user
		if secret.UserID != userID {
			continue
		}

		plaintext, err := DecryptSecret(dek, secret.Ciphertext)
		if err != nil {
			// Log and skip — don't fail the entire activation for one bad secret
			continue
		}

		injected = append(injected, InjectedSecret{
			Type:      secret.Type,
			Name:      secret.Name,
			Metadata:  secret.Metadata,
			Plaintext: string(plaintext),
		})

		// Audit the read
		sid := secret.ID
		s.audit(ctx, userID, "read", &sid, &workspaceID, map[string]string{"name": secret.Name, "reason": "pod_injection"})
	}

	return json.Marshal(injected)
}
