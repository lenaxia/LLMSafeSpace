package secrets

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticKeyProvider_RoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	p, err := NewStaticKeyProvider(key)
	require.NoError(t, err)

	plaintext := []byte("lsp_deadbeef1234567890abcdef01234567890abcdef01234567890abcdef0123")
	ct, err := p.Encrypt(context.Background(), plaintext)
	require.NoError(t, err)
	require.NotEmpty(t, ct)

	decrypted, err := p.Decrypt(context.Background(), ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestStaticKeyProvider_DifferentCiphertextEachEncrypt(t *testing.T) {
	key := make([]byte, 32)
	p, err := NewStaticKeyProvider(key)
	require.NoError(t, err)

	plaintext := []byte("test-plaintext-data")
	ct1, err := p.Encrypt(context.Background(), plaintext)
	require.NoError(t, err)
	ct2, err := p.Encrypt(context.Background(), plaintext)
	require.NoError(t, err)
	assert.NotEqual(t, ct1, ct2, "random nonce should produce different ciphertext")
}

func TestStaticKeyProvider_WrongKeyFailsDecrypt(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 1

	p1, err := NewStaticKeyProvider(key1)
	require.NoError(t, err)
	p2, err := NewStaticKeyProvider(key2)
	require.NoError(t, err)

	ct, err := p1.Encrypt(context.Background(), []byte("secret"))
	require.NoError(t, err)

	_, err = p2.Decrypt(context.Background(), ct)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

func TestStaticKeyProvider_TamperedCiphertextFailsDecrypt(t *testing.T) {
	key := make([]byte, 32)
	p, err := NewStaticKeyProvider(key)
	require.NoError(t, err)

	ct, err := p.Encrypt(context.Background(), []byte("secret"))
	require.NoError(t, err)

	ct[len(ct)-1] ^= 0xFF
	_, err = p.Decrypt(context.Background(), ct)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

func TestStaticKeyProvider_TruncatedCiphertextFails(t *testing.T) {
	key := make([]byte, 32)
	p, err := NewStaticKeyProvider(key)
	require.NoError(t, err)

	_, err = p.Decrypt(context.Background(), []byte{0x01, 0x02})
	assert.ErrorIs(t, err, ErrInvalidCiphertext)
}

func TestNewStaticKeyProvider_RejectsWrongSize(t *testing.T) {
	_, err := NewStaticKeyProvider(make([]byte, 16))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "32 bytes")

	_, err = NewStaticKeyProvider(make([]byte, 64))
	assert.Error(t, err)

	_, err = NewStaticKeyProvider(nil)
	assert.Error(t, err)
}

func TestStaticKeyProvider_CancelledContext(t *testing.T) {
	key := make([]byte, 32)
	p, err := NewStaticKeyProvider(key)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	plaintext := []byte("test-data")
	ct, err := p.Encrypt(ctx, plaintext)
	require.NoError(t, err, "StaticKeyProvider ignores context cancellation (local AES)")

	decrypted, err := p.Decrypt(ctx, ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestStaticKeyProvider_LargePlaintext(t *testing.T) {
	key := make([]byte, 32)
	p, err := NewStaticKeyProvider(key)
	require.NoError(t, err)

	plaintext := make([]byte, 4096)
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	ct, err := p.Encrypt(context.Background(), plaintext)
	require.NoError(t, err)

	decrypted, err := p.Decrypt(context.Background(), ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestSealedKeyProvider_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed-key")
	passphrasePath := filepath.Join(tmpDir, "passphrase")

	passphrase := []byte("correct-horse-battery-staple")
	require.NoError(t, os.WriteFile(passphrasePath, passphrase, 0600))

	rootKey := make([]byte, 32)
	for i := range rootKey {
		rootKey[i] = byte(i)
	}
	require.NoError(t, SealRootKey(sealedPath, passphrase, rootKey))

	p, err := NewSealedKeyProvider(sealedPath, passphrasePath)
	require.NoError(t, err)

	plaintext := []byte("lsp_a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6")
	ct, err := p.Encrypt(context.Background(), plaintext)
	require.NoError(t, err)

	decrypted, err := p.Decrypt(context.Background(), ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestSealedKeyProvider_WrongPassphraseFails(t *testing.T) {
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed-key")
	goodPassPath := filepath.Join(tmpDir, "good-pass")
	badPassPath := filepath.Join(tmpDir, "bad-pass")

	goodPass := []byte("correct-passphrase")
	badPass := []byte("wrong-passphrase")
	require.NoError(t, os.WriteFile(goodPassPath, goodPass, 0600))
	require.NoError(t, os.WriteFile(badPassPath, badPass, 0600))

	rootKey := make([]byte, 32)
	require.NoError(t, SealRootKey(sealedPath, goodPass, rootKey))

	_, err := NewSealedKeyProvider(sealedPath, badPassPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unseal")
}

func TestSealedKeyProvider_MissingSealedKeyFileFails(t *testing.T) {
	tmpDir := t.TempDir()
	passPath := filepath.Join(tmpDir, "passphrase")
	require.NoError(t, os.WriteFile(passPath, []byte("pass"), 0600))

	_, err := NewSealedKeyProvider(filepath.Join(tmpDir, "nonexistent"), passPath)
	assert.Error(t, err)
}

func TestSealedKeyProvider_MissingPassphraseFileFails(t *testing.T) {
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed-key")
	passPath := filepath.Join(tmpDir, "nonexistent")

	require.NoError(t, SealRootKey(sealedPath, []byte("pass"), make([]byte, 32)))

	_, err := NewSealedKeyProvider(sealedPath, passPath)
	assert.Error(t, err)
}

func TestSealedKeyProvider_CorruptedSealedKeyFails(t *testing.T) {
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed-key")
	passPath := filepath.Join(tmpDir, "passphrase")

	require.NoError(t, os.WriteFile(sealedPath, []byte("not-valid-sealed-data"), 0600))
	require.NoError(t, os.WriteFile(passPath, []byte("any-passphrase"), 0600))

	_, err := NewSealedKeyProvider(sealedPath, passPath)
	assert.Error(t, err)
}

func TestSealedKeyProvider_TruncatedSealedKeyFails(t *testing.T) {
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed-key")
	passPath := filepath.Join(tmpDir, "passphrase")

	require.NoError(t, SealRootKey(sealedPath, []byte("pass"), make([]byte, 32)))

	data, err := os.ReadFile(sealedPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(sealedPath, data[:10], 0600))
	require.NoError(t, os.WriteFile(passPath, []byte("pass"), 0600))

	_, err = NewSealedKeyProvider(sealedPath, passPath)
	assert.Error(t, err)
}

func TestSealedKeyProvider_EncryptDecryptWithRealAPIKeyData(t *testing.T) {
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed-key")
	passPath := filepath.Join(tmpDir, "passphrase")
	require.NoError(t, os.WriteFile(passPath, []byte("opensusame"), 0600))

	rootKey, err := GenerateDEK()
	require.NoError(t, err)
	require.NoError(t, SealRootKey(sealedPath, []byte("opensusame"), rootKey))

	p, err := NewSealedKeyProvider(sealedPath, passPath)
	require.NoError(t, err)

	rawAPIKey := "lsp_" + hex.EncodeToString(make([]byte, 32))

	ct, err := p.Encrypt(context.Background(), []byte(rawAPIKey))
	require.NoError(t, err)

	decrypted, err := p.Decrypt(context.Background(), ct)
	require.NoError(t, err)

	require.Equal(t, 1, subtle.ConstantTimeCompare([]byte(rawAPIKey), decrypted))
}

func TestSealRootKey_DeterministicFormat(t *testing.T) {
	tmpDir := t.TempDir()
	sealedPath := filepath.Join(tmpDir, "sealed-key")

	passphrase := []byte("test-passphrase")
	rootKey := make([]byte, 32)

	require.NoError(t, SealRootKey(sealedPath, passphrase, rootKey))

	data, err := os.ReadFile(sealedPath)
	require.NoError(t, err)

	// Format: salt(32) || nonce(12) || ciphertext(32+16) = 92 bytes minimum
	// GCM adds 16-byte tag to 32-byte plaintext = 48 bytes ciphertext
	// Total: 32 + 12 + 48 = 92
	assert.GreaterOrEqual(t, len(data), 92, "sealed key should contain salt + nonce + ciphertext")
}
