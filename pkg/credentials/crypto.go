package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
)

// EncryptionKey represents a single versioned encryption key.
type EncryptionKey struct {
	Version int    `json:"version"`
	Key     []byte `json:"key"` // 32 bytes for AES-256
}

// EncryptionKeySet holds all known encryption keys.
type EncryptionKeySet struct {
	Keys []EncryptionKey `json:"keys"`
}

// ActiveKey returns the highest-version key (used for new writes).
func (ks *EncryptionKeySet) ActiveKey() (*EncryptionKey, error) {
	if len(ks.Keys) == 0 {
		return nil, fmt.Errorf("no encryption keys configured")
	}
	best := &ks.Keys[0]
	for i := range ks.Keys {
		if ks.Keys[i].Version > best.Version {
			best = &ks.Keys[i]
		}
	}
	return best, nil
}

// KeyByVersion returns the key with the given version.
func (ks *EncryptionKeySet) KeyByVersion(version int) (*EncryptionKey, error) {
	for i := range ks.Keys {
		if ks.Keys[i].Version == version {
			return &ks.Keys[i], nil
		}
	}
	return nil, fmt.Errorf("encryption key version %d not found", version)
}

// Encrypt encrypts plaintext using AES-256-GCM with the active key.
// The output is: [1-byte key_version] [nonce] [ciphertext+tag]
func Encrypt(keySet *EncryptionKeySet, plaintext []byte, aad []byte) ([]byte, int, error) {
	active, err := keySet.ActiveKey()
	if err != nil {
		return nil, 0, err
	}
	if len(active.Key) != 32 {
		return nil, 0, fmt.Errorf("encryption key must be 32 bytes, got %d", len(active.Key))
	}

	block, err := aes.NewCipher(active.Key)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)

	// Output: [version_byte][nonce][ciphertext]
	out := make([]byte, 0, 1+len(nonce)+len(ciphertext))
	out = append(out, byte(active.Version))
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	return out, active.Version, nil
}

// Decrypt decrypts data encrypted by Encrypt.
// Reads the key_version prefix to select the correct key.
func Decrypt(keySet *EncryptionKeySet, encrypted []byte, aad []byte) ([]byte, error) {
	if len(encrypted) < 1 {
		return nil, fmt.Errorf("encrypted data too short")
	}

	version := int(encrypted[0])
	key, err := keySet.KeyByVersion(version)
	if err != nil {
		return nil, err
	}
	if len(key.Key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes")
	}

	block, err := aes.NewCipher(key.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(encrypted) < 1+nonceSize {
		return nil, fmt.Errorf("encrypted data too short for nonce")
	}

	nonce := encrypted[1 : 1+nonceSize]
	ciphertext := encrypted[1+nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// ProviderConfig represents the decrypted provider credentials.
type ProviderConfig map[string]ProviderEntry

// ProviderEntry holds credentials for a single provider.
type ProviderEntry struct {
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseUrl,omitempty"`
}

// MarshalProviders serializes provider config to JSON for encryption.
func MarshalProviders(config ProviderConfig) ([]byte, error) {
	return json.Marshal(config)
}

// UnmarshalProviders deserializes provider config from decrypted JSON.
func UnmarshalProviders(data []byte) (ProviderConfig, error) {
	var config ProviderConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return config, nil
}
