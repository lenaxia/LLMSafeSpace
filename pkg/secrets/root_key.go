package secrets

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
)

const (
	sealedSaltSize   = 32
	sealedNonceSize  = 12
	sealedKeyInfoStr = "llmsafespaces-sealed-root"
)

type RootKeyProvider interface {
	Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
}

type StaticKeyProvider struct {
	key []byte
}

func NewStaticKeyProvider(key []byte) (*StaticKeyProvider, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("static key must be 32 bytes, got %d", len(key))
	}
	cp := make([]byte, 32)
	copy(cp, key)
	return &StaticKeyProvider{key: cp}, nil
}

func (p *StaticKeyProvider) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	return EncryptSecret(p.key, plaintext)
}

func (p *StaticKeyProvider) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	return DecryptSecret(p.key, ciphertext)
}

type SealedKeyProvider struct {
	key []byte
}

func NewSealedKeyProvider(sealedKeyPath, passphrasePath string) (*SealedKeyProvider, error) {
	passphrase, err := os.ReadFile(passphrasePath)
	if err != nil {
		return nil, fmt.Errorf("reading passphrase file: %w", err)
	}

	sealedData, err := os.ReadFile(sealedKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading sealed key file: %w", err)
	}

	key, err := unsealKey(passphrase, sealedData)
	if err != nil {
		return nil, fmt.Errorf("unseal: %w", err)
	}

	return &SealedKeyProvider{key: key}, nil
}

func (p *SealedKeyProvider) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	return EncryptSecret(p.key, plaintext)
}

func (p *SealedKeyProvider) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	return DecryptSecret(p.key, ciphertext)
}

func SealRootKey(path string, passphrase, rootKey []byte) error {
	salt := make([]byte, sealedSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generating salt: %w", err)
	}

	kek, err := DeriveKEKFromPassword(passphrase, salt)
	if err != nil {
		return fmt.Errorf("deriving KEK: %w", err)
	}

	ct, err := EncryptSecret(kek, rootKey)
	if err != nil {
		return fmt.Errorf("encrypting root key: %w", err)
	}

	sealed := make([]byte, 0, sealedSaltSize+len(ct))
	sealed = append(sealed, salt...)
	sealed = append(sealed, ct...)

	return os.WriteFile(path, sealed, 0600)
}

func unsealKey(passphrase, sealedData []byte) ([]byte, error) {
	if len(sealedData) < sealedSaltSize+sealedNonceSize+16 {
		return nil, fmt.Errorf("sealed data too short: %d bytes", len(sealedData))
	}

	salt := sealedData[:sealedSaltSize]
	ct := sealedData[sealedSaltSize:]

	kek, err := DeriveKEKFromPassword(passphrase, salt)
	if err != nil {
		return nil, fmt.Errorf("deriving KEK: %w", err)
	}

	rootKey, err := DecryptSecret(kek, ct)
	if err != nil {
		return nil, fmt.Errorf("decrypting sealed key: %w", err)
	}

	if len(rootKey) != 32 {
		return nil, fmt.Errorf("unsealed key must be 32 bytes, got %d", len(rootKey))
	}

	return rootKey, nil
}
