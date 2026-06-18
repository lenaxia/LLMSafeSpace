// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// credentialKeyResolver returns the encryption key for a credential owner
// scope, or nil with an error. Each handler wires its own resolver: admin uses
// the platform KEK, org uses the org KEK, user uses the session DEK.
type credentialKeyResolver func(ctx context.Context) (key []byte, errMsg string, status int)

// probeError bundles an HTTP status and message so getCredentialForProbe can
// report failures without each caller repeating the gin error boilerplate.
type probeError struct {
	status int
	msg    string
}

func (e *probeError) Error() string { return fmt.Sprintf("probe: %s (%d)", e.msg, e.status) }

// getCredentialForProbe fetches a credential row, resolves the encryption key,
// decrypts the ciphertext, and returns the plaintext (LLMProviderData JSON)
// plus the row's saved model context limits for model probing. It returns a
// non-nil probeError when the row is not found, the key is unavailable, or
// decryption fails — the caller writes the HTTP response from the error.
//
// The returned plaintext must be zeroed by the caller once no longer needed
// (probeCredentialModels copies out what it needs but does not zero).
func getCredentialForProbe(
	ctx context.Context,
	store CredentialStore,
	ownerType, ownerID, credID string,
	resolveKey credentialKeyResolver,
) ([]byte, map[string]int, *probeError) {
	row, err := store.GetCredential(ctx, ownerType, ownerID, credID)
	if err != nil {
		return nil, nil, &probeError{status: http.StatusInternalServerError, msg: "failed to get credential"}
	}
	if row == nil {
		return nil, nil, &probeError{status: http.StatusNotFound, msg: "credential not found"}
	}

	key, errMsg, status := resolveKey(ctx)
	if key == nil {
		// errMsg empty means the resolver signalled "not configured" (503).
		if errMsg == "" {
			errMsg = "encryption unavailable"
		}
		return nil, nil, &probeError{status: status, msg: errMsg}
	}

	plaintext, err := secrets.DecryptSecret(key, row.Ciphertext)
	if err != nil {
		return nil, nil, &probeError{status: http.StatusInternalServerError, msg: "failed to decrypt credential"}
	}
	return plaintext, row.ModelContextLimits, nil
}

// encryptCredentialData marshals LLMProviderData for the given provider/API
// key/baseURL, encrypts it with key, and zeros the plaintext buffer so the API
// key does not linger in heap memory longer than necessary. Returns the
// ciphertext or an error describing which step failed (marshal vs. encrypt).
func encryptCredentialData(key []byte, provider, apiKey, baseURL string) ([]byte, error) {
	plaintext, err := json.Marshal(secrets.LLMProviderData{ //nolint:gosec // marshaling for encryption, not API response
		Provider: provider,
		APIKey:   apiKey,
		BaseURL:  baseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode credential: %w", err)
	}
	ciphertext, err := secrets.EncryptSecret(key, plaintext)
	zeroBytes(plaintext)
	if err != nil {
		return nil, fmt.Errorf("encryption failed: %w", err)
	}
	return ciphertext, nil
}
