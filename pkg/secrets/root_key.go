package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"sort"
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

// VersionedProvider is implemented by providers that expose an active key
// version (US-50.3/50.4). Callers that need the version for key_version column
// writes assert this interface on the concrete provider — it is intentionally
// NOT on RootKeyProvider so a future external provider (Vault Transit, which
// handles versioning server-side) doesn't need to implement it.
type VersionedProvider interface {
	ActiveVersion() int
}

// ActiveVersionOf returns the active key version of a provider, or 1 if the
// provider does not implement VersionedProvider (e.g. nil or a future external
// provider). This is the safe default — version 1 is the initial migration
// default for all tables.
func ActiveVersionOf(p RootKeyProvider) int {
	if p == nil {
		return 1
	}
	if vp, ok := p.(VersionedProvider); ok {
		v := vp.ActiveVersion()
		if v > 0 {
			return v
		}
	}
	return 1
}

// keyEntry pairs a versioned key with its version number. The provider holds
// a slice sorted by version descending so Decrypt tries the newest first.
type keyEntry struct {
	version int
	key     []byte
}

// StaticKeyProvider holds one or more versioned keys. Encrypt always uses the
// highest-version (active) key; Decrypt tries each key newest-to-oldest and
// returns the first success. This enables zero-downtime KEK rotation (US-50.4,
// design D4): during the transition window the provider holds both old and new
// keys so ciphertexts encrypted under either version decrypt correctly.
type StaticKeyProvider struct {
	entries []keyEntry // sorted by version descending
}

// NewStaticKeyProvider constructs a single-key provider at version 1. This is
// the backward-compatible constructor used everywhere except the rotation window.
func NewStaticKeyProvider(key []byte) (*StaticKeyProvider, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("static key must be 32 bytes, got %d", len(key))
	}
	cp := make([]byte, 32)
	copy(cp, key)
	return &StaticKeyProvider{entries: []keyEntry{{version: 1, key: cp}}}, nil
}

// NewStaticKeyProviderMultiVersion constructs a multi-key provider for the
// rotation transition window (US-50.4). activeVersion is the highest version
// (the one Encrypt uses); keyByVersion maps every version to its key material.
// At least one entry at activeVersion must exist. Entries are stored sorted by
// version descending so Decrypt tries the newest first.
func NewStaticKeyProviderMultiVersion(activeVersion int, keyByVersion map[int][]byte) (*StaticKeyProvider, error) {
	if len(keyByVersion) == 0 {
		return nil, fmt.Errorf("at least one key entry is required")
	}
	activeKey, ok := keyByVersion[activeVersion]
	if !ok {
		return nil, fmt.Errorf("activeVersion %d not present in keyByVersion map", activeVersion)
	}
	if len(activeKey) != 32 {
		return nil, fmt.Errorf("key for version %d must be 32 bytes, got %d", activeVersion, len(activeKey))
	}
	for ver, k := range keyByVersion {
		if len(k) != 32 {
			return nil, fmt.Errorf("key for version %d must be 32 bytes, got %d", ver, len(k))
		}
	}
	entries := make([]keyEntry, 0, len(keyByVersion))
	for ver, k := range keyByVersion {
		cp := make([]byte, 32)
		copy(cp, k)
		entries = append(entries, keyEntry{version: ver, key: cp})
	}
	// Sort descending by version so Decrypt tries the newest (active) key first.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].version > entries[j].version
	})
	return &StaticKeyProvider{entries: entries}, nil
}

// ActiveVersion returns the highest version the provider can encrypt with
// (US-50.3 uses this to populate key_version columns on encrypt).
func (p *StaticKeyProvider) ActiveVersion() int {
	if len(p.entries) == 0 {
		return 0
	}
	return p.entries[0].version
}

func (p *StaticKeyProvider) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	return EncryptSecret(p.entries[0].key, plaintext)
}

func (p *StaticKeyProvider) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	var lastErr error
	for _, e := range p.entries {
		pt, err := DecryptSecret(e.key, ciphertext)
		if err == nil {
			return pt, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// SealedKeyProvider holds the unsealed root key in process memory. It defends
// against attackers who can read the sealed file or the node disk but NOT the
// passphrase: the on-disk file is Argon2id-wrapped and is useless without the
// passphrase. It does NOT defend against process-level compromise of the API
// pod — once the key is unsealed at boot it lives in memory, and an attacker
// who can run code in the pod can call Decrypt exactly as the application does.
// See pkg/secrets/README.md for the full threat model.
//
// US-50.4 multi-key support (NewStaticKeyProviderMultiVersion) is NOT mirrored
// here yet. The sealed provider is constructed once at boot from a single
// sealed file; multi-file rotation-window support for the sealed path will be
// added alongside US-50.5 (rotate-kek CLI) when the rotation workflow is
// exercised end-to-end. The StaticKeyProvider covers the default Helm path.
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
