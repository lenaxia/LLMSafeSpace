// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AuditWriter is the narrow interface for writing audit entries. Implemented
// by SecretStore, AsyncAuditLogger, and test doubles. US-50.12.
type AuditWriter interface {
	LogAudit(ctx context.Context, entry *AuditEntry) error
}

// DecryptAuditUserKey is the context key for the user ID to attribute decrypt
// audit entries to. Handlers that have a user context set this via
// context.WithValue; absent it, "_system" is used.
type decryptAuditUserKey struct{}

// ContextWithDecryptUser returns a context carrying the user ID for decrypt
// audit attribution. Callers should pass the resulting context to the
// provider's Decrypt method.
func ContextWithDecryptUser(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, decryptAuditUserKey{}, userID)
}

// AuditedProvider wraps a RootKeyProvider and logs every Decrypt call to the
// audit writer (US-50.12). Encrypt is NOT logged — it is not a sensitive read.
// The audit write is fire-and-forget (goroutine per call) so Decrypt never
// blocks on the audit pipeline. In production the AuditWriter is
// AsyncAuditLogger (already buffered), so the goroutine's LogAudit is a
// near-instant channel send.
//
// What is logged: caller label, user ID (from context or "_system"),
// key version, timestamp, success/failure. Metadata is a JSON object.
//
// What is NOT logged: plaintext, ciphertext, key material — never.
type AuditedProvider struct {
	inner RootKeyProvider
	audit AuditWriter
	label string // "provider-credentials", "org-credentials", "master-kek"
}

// NewAuditedProvider wraps inner with an audit-decorating provider. label
// identifies the purpose string for log attribution.
func NewAuditedProvider(inner RootKeyProvider, audit AuditWriter, label string) *AuditedProvider {
	return &AuditedProvider{inner: inner, audit: audit, label: label}
}

// ActiveVersion delegates to the inner provider so the wrapper satisfies
// VersionedProvider. This is load-bearing: production callers invoke
// ActiveVersionOf(provider) at encrypt time to stamp the key_version column
// (auth.go, admin_provider_credentials.go, org_credentials.go). Without this
// delegation, wrapping with NewAuditedProvider would silently downgrade every
// key_version to the default 1, corrupting rotation tracking.
func (p *AuditedProvider) ActiveVersion() int {
	return ActiveVersionOf(p.inner)
}

func (p *AuditedProvider) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	return p.inner.Encrypt(ctx, plaintext)
}

func (p *AuditedProvider) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	pt, err := p.inner.Decrypt(ctx, ciphertext)

	// Build the entry synchronously so the timestamp and key version are
	// accurate at the time of decryption, not at write time.
	entry := p.buildEntry(ctx, err == nil)

	// Fire-and-forget — the audit write must never block decrypt. Using
	// context.Background() because the request context may be canceled after
	// Decrypt returns; the audit entry should outlive the request.
	go func() { //nolint:gosec,contextcheck // G118: intentional — audit must outlive the request ctx
		_ = p.audit.LogAudit(context.Background(), entry)
	}()

	return pt, err
}

func (p *AuditedProvider) buildEntry(ctx context.Context, success bool) *AuditEntry {
	userID := "_system"
	if v, ok := ctx.Value(decryptAuditUserKey{}).(string); v != "" && ok {
		userID = v
	}

	keyVersion := ActiveVersionOf(p.inner)

	meta := map[string]any{
		"label":       p.label,
		"success":     success,
		"key_version": keyVersion,
	}
	metaJSON, _ := json.Marshal(meta)

	return &AuditEntry{
		UserID:    userID,
		Action:    fmt.Sprintf("decrypt:%s", p.label),
		Metadata:  metaJSON,
		Timestamp: time.Now(),
	}
}
