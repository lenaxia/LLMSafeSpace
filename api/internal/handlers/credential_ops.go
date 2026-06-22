// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lenaxia/llmsafespaces/pkg/secrets"
)

// credentialDecryptResolver returns a decrypt closure for a credential owner
// scope, or nil with an error. Each handler wires its own resolver: admin uses
// the platform provider, org uses the org provider, user uses the session DEK.
// US-50.2: returns a decrypt function (not a raw key) so all crypto flows
// through RootKeyProvider.
type credentialDecryptResolver func(ctx context.Context) (decrypt func(context.Context, []byte) ([]byte, error), errMsg string, status int)

// probeError bundles an HTTP status and message so getCredentialForProbe can
// report failures without each caller repeating the gin error boilerplate.
type probeError struct {
	status int
	msg    string
}

func (e *probeError) Error() string { return fmt.Sprintf("probe: %s (%d)", e.msg, e.status) }

// getCredentialForProbe fetches a credential row, resolves a decrypt closure,
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
	resolveDecrypt credentialDecryptResolver,
) ([]byte, map[string]int, *probeError) {
	row, err := store.GetCredential(ctx, ownerType, ownerID, credID)
	if err != nil {
		return nil, nil, &probeError{status: http.StatusInternalServerError, msg: "failed to get credential"}
	}
	if row == nil {
		return nil, nil, &probeError{status: http.StatusNotFound, msg: "credential not found"}
	}

	decrypt, errMsg, status := resolveDecrypt(ctx)
	if decrypt == nil {
		// errMsg empty means the resolver signaled "not configured" (503).
		if errMsg == "" {
			errMsg = "encryption unavailable"
		}
		return nil, nil, &probeError{status: status, msg: errMsg}
	}

	plaintext, err := decrypt(ctx, row.Ciphertext)
	if err != nil {
		return nil, nil, &probeError{status: http.StatusInternalServerError, msg: "failed to decrypt credential"}
	}
	return plaintext, row.ModelContextLimits, nil
}

// encryptCredentialData marshals LLMProviderData for the given provider/API
// key/baseURL, encrypts it via the provided encrypt closure, and zeros the
// plaintext buffer so the API key does not linger in heap memory longer than
// necessary. Admin/org handlers pass provider.Encrypt; the user handler passes
// a DEK-wrapping closure (US-50.2: unifies all encrypt paths through closures).
func encryptCredentialData(ctx context.Context, encrypt func(context.Context, []byte) ([]byte, error), llmProvider, apiKey, baseURL string) ([]byte, error) {
	plaintext, err := json.Marshal(secrets.LLMProviderData{ //nolint:gosec // marshaling for encryption, not API response
		Provider: llmProvider,
		APIKey:   apiKey,
		BaseURL:  baseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode credential: %w", err)
	}
	ciphertext, err := encrypt(ctx, plaintext)
	zeroBytes(plaintext)
	if err != nil {
		return nil, fmt.Errorf("encryption failed: %w", err)
	}
	return ciphertext, nil
}
