// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAuditCapture is a test-only AuditWriter that records every LogAudit call.
type fakeAuditCapture struct {
	mu      sync.Mutex
	entries []*AuditEntry
}

func (f *fakeAuditCapture) LogAudit(_ context.Context, entry *AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *entry
	f.entries = append(f.entries, &cp)
	return nil
}

func (f *fakeAuditCapture) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

func (f *fakeAuditCapture) last() *AuditEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.entries) == 0 {
		return nil
	}
	return f.entries[len(f.entries)-1]
}

func newAuditedProviderForTest(t *testing.T, label string) (*AuditedProvider, *fakeAuditCapture) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	inner, err := NewStaticKeyProvider(key)
	require.NoError(t, err)
	fake := &fakeAuditCapture{}
	return &AuditedProvider{inner: inner, audit: fake, label: label}, fake
}

// TestAuditedProvider_Decrypt_LogsEntry proves every successful Decrypt produces
// exactly one audit entry with the expected metadata.
func TestAuditedProvider_Decrypt_LogsEntry(t *testing.T) {
	provider, fake := newAuditedProviderForTest(t, "provider-credentials")

	ctx := context.Background()
	plaintext := []byte("secret-api-key-value")
	ct, err := provider.Encrypt(ctx, plaintext)
	require.NoError(t, err)

	dec, err := provider.Decrypt(ctx, ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, dec)

	// Fire-and-forget audit: give the goroutine time to land.
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 1, fake.len(), "Decrypt must produce exactly one audit entry")
	entry := fake.last()
	assert.Equal(t, "decrypt:provider-credentials", entry.Action, "audit action should carry the decrypt:label format")
	assert.NotEmpty(t, entry.UserID, "audit entry needs a user identifier")
	assert.False(t, entry.Timestamp.IsZero(), "timestamp must be set")

	// Metadata must contain success=true and key_version.
	var meta map[string]any
	require.NoError(t, json.Unmarshal(entry.Metadata, &meta))
	assert.Equal(t, true, meta["success"], "successful decrypt must log success=true")
	assert.NotNil(t, meta["key_version"], "key_version must be in metadata")
	assert.Equal(t, "provider-credentials", meta["label"])
}

// TestAuditedProvider_DecryptFailure_LogsEntryWithSuccessFalse proves a failed
// decrypt (wrong key / corrupted ciphertext) is also logged with success=false.
func TestAuditedProvider_DecryptFailure_LogsEntryWithSuccessFalse(t *testing.T) {
	provider, fake := newAuditedProviderForTest(t, "org-credentials")

	// Ciphertext encrypted with a different key — decrypt will fail.
	rogueKey := make([]byte, 32)
	for i := range rogueKey {
		rogueKey[i] = byte(i + 99)
	}
	badCT, err := EncryptSecret(rogueKey, []byte("rogue"))
	require.NoError(t, err)

	_, err = provider.Decrypt(context.Background(), badCT)
	require.Error(t, err, "decrypt with wrong key must fail")

	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 1, fake.len(), "failed decrypt must still produce an audit entry")
	entry := fake.last()
	var meta map[string]any
	require.NoError(t, json.Unmarshal(entry.Metadata, &meta))
	assert.Equal(t, false, meta["success"], "failed decrypt must log success=false")
}

// TestAuditedProvider_Encrypt_NotLogged proves Encrypt does NOT produce an audit
// entry — encrypt is not a sensitive read operation.
func TestAuditedProvider_Encrypt_NotLogged(t *testing.T) {
	provider, fake := newAuditedProviderForTest(t, "provider-credentials")

	_, err := provider.Encrypt(context.Background(), []byte("some-data"))
	require.NoError(t, err)

	assert.Equal(t, 0, fake.len(), "Encrypt must NOT produce an audit entry")
}

// TestAuditedProvider_NoKeyMaterialInLog proves no byte sequence from plaintext
// or ciphertext appears in the audit entry. This is the critical security
// invariant — the audit log must never become a secondary secret store.
func TestAuditedProvider_NoKeyMaterialInLog(t *testing.T) {
	provider, fake := newAuditedProviderForTest(t, "provider-credentials")

	plaintext := []byte("lsp_uniquePlaintextMarker1234567890")
	ct, err := provider.Encrypt(context.Background(), plaintext)
	require.NoError(t, err)

	_, err = provider.Decrypt(context.Background(), ct)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	entry := fake.last()
	require.NotNil(t, entry, "audit entry must exist")

	// Serialize the entire audit entry to a byte slice and grep for secrets.
	entryBytes, err := json.Marshal(entry)
	require.NoError(t, err)

	assert.False(t, bytes.Contains(entryBytes, plaintext),
		"audit entry must NOT contain the plaintext")
	assert.False(t, bytes.Contains(entryBytes, ct),
		"audit entry must NOT contain the ciphertext")
}

// TestAuditedProvider_AsyncDoesNotBlockDecrypt proves the audit write is
// non-blocking — Decrypt returns before the audit entry is persisted. Uses a
// slow AuditWriter that sleeps, proving Decrypt doesn't wait for it.
func TestAuditedProvider_AsyncDoesNotBlockDecrypt(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	inner, err := NewStaticKeyProvider(key)
	require.NoError(t, err)

	slowAudit := &slowAuditWriter{delay: 100 * time.Millisecond}
	provider := &AuditedProvider{inner: inner, audit: slowAudit, label: "provider-credentials"}

	ct, err := provider.Encrypt(context.Background(), []byte("test"))
	require.NoError(t, err)

	start := time.Now()
	_, err = provider.Decrypt(context.Background(), ct)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 50*time.Millisecond,
		"Decrypt must return before the slow audit write completes (async)")
}

// slowAuditWriter sleeps before accepting an entry, simulating a slow DB.
type slowAuditWriter struct {
	delay time.Duration
}

func (s *slowAuditWriter) LogAudit(ctx context.Context, entry *AuditEntry) error {
	select {
	case <-time.After(s.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestAuditedProvider_DelegatesActiveVersion is the load-bearing correctness
// test for wiring AuditedProvider into production (issue #366). Production
// callers invoke secrets.ActiveVersionOf(provider) at encrypt time to stamp
// the key_version column (auth.go, admin_provider_credentials.go,
// org_credentials.go). ActiveVersionOf does a VersionedProvider type
// assertion; if AuditedProvider did not satisfy it, wrapping would silently
// downgrade every key_version to the default 1 — corrupting rotation.
// Asserts the wrapper preserves the inner provider's version (both single
// and multi-version cases).
func TestAuditedProvider_DelegatesActiveVersion(t *testing.T) {
	t.Run("single-version inner", func(t *testing.T) {
		inner, err := NewStaticKeyProvider(make([]byte, 32))
		require.NoError(t, err)
		wrapped := NewAuditedProvider(inner, &fakeAuditCapture{}, "provider-credentials")

		assert.Equal(t, ActiveVersionOf(inner), ActiveVersionOf(wrapped),
			"wrapping must preserve the inner provider's active version")
		assert.Equal(t, 1, ActiveVersionOf(wrapped))
	})

	t.Run("multi-version inner (active=2)", func(t *testing.T) {
		k1 := make([]byte, 32)
		k2 := make([]byte, 32)
		k2[0] = 0xff
		inner, err := NewStaticKeyProviderMultiVersion(2, map[int][]byte{1: k1, 2: k2})
		require.NoError(t, err)
		wrapped := NewAuditedProvider(inner, &fakeAuditCapture{}, "api-keys")

		assert.Equal(t, 2, ActiveVersionOf(wrapped),
			"wrapping a multi-version provider must report the inner's active version (2), not the default 1")
	})

	t.Run("nil-safe inner", func(t *testing.T) {
		wrapped := &AuditedProvider{inner: nil, audit: &fakeAuditCapture{}, label: "x"}
		assert.Equal(t, 1, ActiveVersionOf(wrapped),
			"ActiveVersionOf must remain nil-safe through the wrapper (returns default 1)")
	})
}
