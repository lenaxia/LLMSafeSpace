package secrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticKeyProvider_MultiKey_DecryptRoutesByTrial(t *testing.T) {
	keyA := make([]byte, 32) // version 1
	for i := range keyA {
		keyA[i] = byte(i + 1)
	}
	keyB := make([]byte, 32) // version 2 (active)
	for i := range keyB {
		keyB[i] = byte(i + 50)
	}

	p, err := NewStaticKeyProviderMultiVersion(2, map[int][]byte{1: keyA, 2: keyB})
	require.NoError(t, err)

	// Encrypt with keyA directly (simulating an old-version ciphertext).
	ctA, err := EncryptSecret(keyA, []byte("old-version-secret"))
	require.NoError(t, err)

	// Encrypt with keyB directly (simulating a new-version ciphertext).
	ctB, err := EncryptSecret(keyB, []byte("new-version-secret"))
	require.NoError(t, err)

	// Provider must decrypt both — old via trial, new via active key.
	decA, err := p.Decrypt(context.Background(), ctA)
	require.NoError(t, err)
	assert.Equal(t, "old-version-secret", string(decA))

	decB, err := p.Decrypt(context.Background(), ctB)
	require.NoError(t, err)
	assert.Equal(t, "new-version-secret", string(decB))
}

func TestStaticKeyProvider_MultiKey_EncryptUsesHighestVersion(t *testing.T) {
	keyA := make([]byte, 32)
	for i := range keyA {
		keyA[i] = byte(i + 1)
	}
	keyB := make([]byte, 32)
	for i := range keyB {
		keyB[i] = byte(i + 50)
	}

	p, err := NewStaticKeyProviderMultiVersion(2, map[int][]byte{1: keyA, 2: keyB})
	require.NoError(t, err)

	ct, err := p.Encrypt(context.Background(), []byte("active-secret"))
	require.NoError(t, err)

	// The ciphertext must decrypt with keyB (highest/active version), NOT keyA.
	_, err = DecryptSecret(keyB, ct)
	require.NoError(t, err, "Encrypt must use the highest-version key")

	_, err = DecryptSecret(keyA, ct)
	assert.ErrorIs(t, err, ErrDecryptionFailed, "Encrypt must NOT use the lower-version key")
}

func TestStaticKeyProvider_MultiKey_ActiveVersion_ReturnsHighest(t *testing.T) {
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	keyC := make([]byte, 32)

	p, err := NewStaticKeyProviderMultiVersion(3, map[int][]byte{1: keyA, 2: keyB, 3: keyC})
	require.NoError(t, err)
	assert.Equal(t, 3, p.ActiveVersion())

	p2, err := NewStaticKeyProviderMultiVersion(1, map[int][]byte{1: keyA})
	require.NoError(t, err)
	assert.Equal(t, 1, p2.ActiveVersion())
}

func TestStaticKeyProvider_MultiKey_WrongKeyReturnsError(t *testing.T) {
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	rogueKey := make([]byte, 32)
	for i := range rogueKey {
		rogueKey[i] = byte(i + 99)
	}

	p, err := NewStaticKeyProviderMultiVersion(2, map[int][]byte{1: keyA, 2: keyB})
	require.NoError(t, err)

	ct, err := EncryptSecret(rogueKey, []byte("rogue-secret"))
	require.NoError(t, err)

	_, err = p.Decrypt(context.Background(), ct)
	assert.ErrorIs(t, err, ErrDecryptionFailed, "ciphertext encrypted with a key not in the provider must fail")
}

func TestStaticKeyProvider_SingleKey_BackwardCompatible(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}

	// The existing single-key constructor must still work unchanged.
	p, err := NewStaticKeyProvider(key)
	require.NoError(t, err)
	assert.Equal(t, 1, p.ActiveVersion(), "single-key provider defaults to version 1")

	plaintext := []byte("backward-compat-test")
	ct, err := p.Encrypt(context.Background(), plaintext)
	require.NoError(t, err)

	dec, err := p.Decrypt(context.Background(), ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, dec)
}

func TestNewStaticKeyProviderMultiVersion_EmptyMap_Error(t *testing.T) {
	_, err := NewStaticKeyProviderMultiVersion(1, map[int][]byte{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one key")
}

func TestNewStaticKeyProviderMultiVersion_ActiveVersionNotInMap_Error(t *testing.T) {
	key := make([]byte, 32)
	_, err := NewStaticKeyProviderMultiVersion(2, map[int][]byte{1: key})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "activeVersion 2 not present")
}

func TestNewStaticKeyProviderMultiVersion_ActiveKeyWrongSize_Error(t *testing.T) {
	shortKey := make([]byte, 16)
	_, err := NewStaticKeyProviderMultiVersion(1, map[int][]byte{1: shortKey})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32 bytes")
}

func TestNewStaticKeyProviderMultiVersion_NonActiveKeyWrongSize_Error(t *testing.T) {
	goodKey := make([]byte, 32)
	shortKey := make([]byte, 16)
	_, err := NewStaticKeyProviderMultiVersion(2, map[int][]byte{1: shortKey, 2: goodKey})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32 bytes")
}

func TestNewStaticKeyProviderMultiVersion_SingleVersion_Works(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	p, err := NewStaticKeyProviderMultiVersion(1, map[int][]byte{1: key})
	require.NoError(t, err)
	assert.Equal(t, 1, p.ActiveVersion())

	pt := []byte("single-multi-version")
	ct, err := p.Encrypt(context.Background(), pt)
	require.NoError(t, err)
	dec, err := p.Decrypt(context.Background(), ct)
	require.NoError(t, err)
	assert.Equal(t, pt, dec)
}
