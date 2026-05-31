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
// and returns the JSON payload for the ephemeral K8s Secret. Called
// during workspace activation and by the bind-time auto-push.
//
// Verifies the caller owns the workspace before reading the binding
// set; without this check, a foreign-workspace request would still
// reach the store query and the empty-injected-payload response time
// could leak workspace existence (validator pass-4 finding PARTIAL-1).
func (s *SecretService) PrepareSecretsForInjection(ctx context.Context, userID, sessionID, workspaceID string) ([]byte, error) {
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
