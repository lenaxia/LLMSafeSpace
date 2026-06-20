package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
)

const (
	sealedSaltSize   = 32
	sealedNonceSize  = 12
	sealedKeyInfoStr = "llmsafespaces-sealed-root"
	sealedMagicV1    = "LSKP-S"
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

// SealedKeyProvider holds the unsealed root key in process memory. It defends
// against attackers who can read the sealed file or the node disk but NOT the
// passphrase: the on-disk file is Argon2id-wrapped and is useless without the
// passphrase. It does NOT defend against process-level compromise of the API
// pod — once the key is unsealed at boot it lives in memory, and an attacker
// who can run code in the pod can call Decrypt exactly as the application does.
// See pkg/secrets/README.md for the full threat model.
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

	kek, err := DeriveSealedKEK(passphrase, salt, sealedKeyInfoStr)
	if err != nil {
		return fmt.Errorf("deriving KEK: %w", err)
	}

	ct, err := EncryptSecret(kek, rootKey)
	if err != nil {
		return fmt.Errorf("encrypting root key: %w", err)
	}

	// V1 format (US-50.11): magic || salt || ciphertext. The magic marks the
	// info-domain-separated KEK derivation so unsealKey can route V1 vs the
	// legacy salt||ciphertext layout.
	sealed := make([]byte, 0, len(sealedMagicV1)+sealedSaltSize+len(ct))
	sealed = append(sealed, []byte(sealedMagicV1)...)
	sealed = append(sealed, salt...)
	sealed = append(sealed, ct...)

	return os.WriteFile(path, sealed, 0600)
}

// unsealKey routes by magic prefix. V1 files (magic "LSKP-S", US-50.11) use
// the info-domain-separated KEK; files without the prefix are legacy V0 and
// use plain Argon2id without an HKDF info string.
//
// A random V0 salt starting with the ASCII bytes "LSKP-S" would misdetect as
// V1; that is a 1/2^48 event and would surface as a clean decrypt failure
// (wrong KEK), never silent data corruption.
func unsealKey(passphrase, sealedData []byte) ([]byte, error) {
	if bytes.HasPrefix(sealedData, []byte(sealedMagicV1)) {
		return unsealKeyV1(passphrase, sealedData)
	}
	return unsealKeyV0(passphrase, sealedData)
}

// unsealKeyV1 reads the V1 layout: magic || salt || ciphertext.
func unsealKeyV1(passphrase, sealedData []byte) ([]byte, error) {
	body := sealedData[len(sealedMagicV1):]
	if len(body) < sealedSaltSize+sealedNonceSize+16 {
		return nil, fmt.Errorf("sealed data too short: %d bytes", len(sealedData))
	}
	salt := body[:sealedSaltSize]
	ct := body[sealedSaltSize:]

	kek, err := DeriveSealedKEK(passphrase, salt, sealedKeyInfoStr)
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

// unsealKeyV0 reads the legacy layout: salt || ciphertext, with the KEK
// derived via Argon2id without an HKDF info string. Retained so sealed-key
// files produced before US-50.11 continue to unseal.
func unsealKeyV0(passphrase, sealedData []byte) ([]byte, error) {
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
