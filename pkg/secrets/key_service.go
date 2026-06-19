// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"time"

	pkginterfaces "github.com/lenaxia/llmsafespaces/pkg/interfaces"
)

// UserKeyRecord represents a row in the user_keys table.
type UserKeyRecord struct {
	UserID             string
	KeyVersion         int
	WrappedDEK         []byte
	WrappedDEKRecovery []byte // nil if user opted out
	Salt               []byte
	RecoverySalt       []byte // nil if user opted out
	CreatedAt          time.Time
	RotatedAt          *time.Time
}

// KeyStore abstracts database operations for user keys.
type KeyStore interface {
	GetUserKey(ctx context.Context, userID string) (*UserKeyRecord, error)
	CreateUserKey(ctx context.Context, record *UserKeyRecord) error
	UpdateWrappedDEK(ctx context.Context, userID string, wrappedDEK []byte, salt []byte, keyVersion int) error
	UpdateWrappedDEKRecovery(ctx context.Context, userID string, wrappedDEKRecovery []byte, recoverySalt []byte) error
}

// DEKCache abstracts session-based DEK caching (Redis).
type DEKCache interface {
	CacheDEK(ctx context.Context, sessionID string, dek []byte, ttl time.Duration) error
	GetDEK(ctx context.Context, sessionID string) ([]byte, error)
	EvictDEK(ctx context.Context, sessionID string) error
}

// KeyService manages user key lifecycle.
type KeyService struct {
	store           KeyStore
	cache           DEKCache
	secretStore     SecretStore
	logger          pkginterfaces.LoggerInterface
	apiKeyStore     APIKeyStore
	rootKeyProvider RootKeyProvider
}

// APIKeyRecord is the subset of API key data needed for DEK re-wrap.
type APIKeyRecord struct {
	ID            string
	WrappedDEK    []byte
	KekSalt       []byte
	KeyCiphertext []byte
	DecryptAccess bool
}

// APIKeyStore abstracts database operations for API key DEK re-wrap.
type APIKeyStore interface {
	ListAPIKeysWithDecrypt(ctx context.Context, userID string) ([]*APIKeyRecord, error)
	UpdateAPIKeyDEK(ctx context.Context, keyID string, wrappedDEK, kekSalt []byte, synced bool) error
}

// NewKeyService creates a new KeyService.
func NewKeyService(store KeyStore, cache DEKCache) *KeyService {
	return &KeyService{store: store, cache: cache}
}

// SetAPIKeyStore wires the API key store for DEK re-wrap on rotation.
func (s *KeyService) SetAPIKeyStore(store APIKeyStore, provider RootKeyProvider) {
	s.apiKeyStore = store
	s.rootKeyProvider = provider
}

// SetLogger installs the logger used to surface non-fatal failures
// (e.g. cache-evict errors during password change). Optional; if
// nil, those events are silent. Validator pass-5 finding N-3.
//
// Note: ChangePassword's evict-failure log includes the sessionID
// (JWT jti). The jti is sensitive — an attacker with log read
// access can correlate user activity across requests, though it
// does NOT enable token replay (the JWT signature is never logged).
// Volume is bounded to Redis-outage events. If the log retention
// crosses a tenant boundary, hash sessionID before logging.
func (s *KeyService) SetLogger(l pkginterfaces.LoggerInterface) {
	s.logger = l
}

// SetSecretStore wires the SecretStore used by RotateKeyWithPassword to
// re-encrypt every user_secrets row under the new DEK. Without this, the
// rotate endpoint refuses to run rather than orphan secret rows under a
// discarded DEK (Bug 9 in worklog 0085).
//
// Once set, the store cannot be silently reassigned: a silent
// reassignment would mean RotateKeyWithPassword ignores secrets owned
// by an abandoned store — exactly the Bug 9 hazard. Calling
// SetSecretStore twice with different stores panics; calling with the
// same store (idempotent re-init) is allowed.
func (s *KeyService) SetSecretStore(store SecretStore) {
	if s.secretStore != nil && s.secretStore != store {
		panic("KeyService.SetSecretStore called twice with different stores; refusing to silently rebind")
	}
	s.secretStore = store
}

// InitializeUserKeys generates a DEK and wraps it with the user's password-derived KEK.
// Called during account creation or first secret creation for existing users.
// Returns the recovery key (hex-encoded) that must be displayed to the user once.
func (s *KeyService) InitializeUserKeys(ctx context.Context, userID string, password []byte) (recoveryKeyHex string, err error) {
	dek, err := GenerateDEK()
	if err != nil {
		return "", fmt.Errorf("generate DEK: %w", err)
	}

	salt, err := GenerateSalt()
	if err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	kek, err := DeriveKEKFromPassword(password, salt)
	if err != nil {
		return "", fmt.Errorf("derive KEK: %w", err)
	}
	defer zeroBytes(kek)

	wrappedDEK, err := WrapDEK(kek, dek)
	if err != nil {
		return "", fmt.Errorf("wrap DEK: %w", err)
	}

	// Generate recovery key
	recoveryKey, err := GenerateRecoveryKey()
	if err != nil {
		return "", fmt.Errorf("generate recovery key: %w", err)
	}

	recoverySalt, err := GenerateSalt()
	if err != nil {
		return "", fmt.Errorf("generate recovery salt: %w", err)
	}

	recoveryKEK, err := DeriveKEKFromKey(recoveryKey, recoverySalt, recInfo)
	if err != nil {
		return "", fmt.Errorf("derive recovery KEK: %w", err)
	}
	defer zeroBytes(recoveryKEK)

	wrappedDEKRecovery, err := WrapDEK(recoveryKEK, dek)
	if err != nil {
		return "", fmt.Errorf("wrap DEK with recovery: %w", err)
	}

	record := &UserKeyRecord{
		UserID:             userID,
		KeyVersion:         1,
		WrappedDEK:         wrappedDEK,
		WrappedDEKRecovery: wrappedDEKRecovery,
		Salt:               salt,
		RecoverySalt:       recoverySalt,
		CreatedAt:          time.Now(),
	}

	if err := s.store.CreateUserKey(ctx, record); err != nil {
		return "", fmt.Errorf("store user key: %w", err)
	}

	return hex.EncodeToString(recoveryKey), nil
}

// UnlockDEK derives the KEK from the password, unwraps the DEK, and caches it.
// Called during login. sessionID is the JWT's jti claim.
func (s *KeyService) UnlockDEK(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration) error {
	record, err := s.store.GetUserKey(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user key: %w", err)
	}
	if record == nil {
		// User has no keys yet (legacy user who hasn't created secrets)
		return nil
	}

	kek, err := DeriveKEKFromPassword(password, record.Salt)
	if err != nil {
		return fmt.Errorf("derive KEK: %w", err)
	}
	defer zeroBytes(kek)

	dek, err := UnwrapDEK(kek, record.WrappedDEK)
	if err != nil {
		return fmt.Errorf("unwrap DEK: %w", err)
	}

	if err := s.cache.CacheDEK(ctx, sessionID, dek, ttl); err != nil {
		return fmt.Errorf("cache DEK: %w", err)
	}

	return nil
}

// EvictDEK removes the cached DEK for a session. Called on logout/expiry.
func (s *KeyService) EvictDEK(ctx context.Context, sessionID string) error {
	return s.cache.EvictDEK(ctx, sessionID)
}

// CacheDEK stores a DEK in the session cache. Used by API key auth to cache
// an unwrapped DEK under a deterministic sessionID.
func (s *KeyService) CacheDEK(ctx context.Context, sessionID string, dek []byte, ttl time.Duration) error {
	return s.cache.CacheDEK(ctx, sessionID, dek, ttl)
}

// GetDEK retrieves the cached DEK for a session.
func (s *KeyService) GetDEK(ctx context.Context, sessionID string) ([]byte, error) {
	dek, err := s.cache.GetDEK(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if dek == nil {
		return nil, errors.New("DEK not available: session expired or not unlocked")
	}
	return dek, nil
}

// DEKAvailable checks if a DEK is cached for the given session.
func (s *KeyService) DEKAvailable(ctx context.Context, sessionID string) bool {
	dek, err := s.cache.GetDEK(ctx, sessionID)
	return err == nil && dek != nil
}

// ChangePassword re-wraps the DEK with a new password-derived KEK.
// Requires the old password to unwrap first. After the wrap is
// updated, the cached DEK for sessionID (the caller's current
// session) is evicted so the next request must re-Unlock with the
// new password — without this eviction a thief who has the JWT
// continues to read secrets via the cached DEK even after the user
// "rotates the password to be safe" (validator pass-3 finding P-1).
//
// LIMITATION: this only evicts the caller's session. A user with
// multiple active sessions on different devices retains those
// cached DEKs until they expire naturally. We document the
// limitation in the API rather than rebuild the cache for cross-
// session enumeration.
//
// sessionID may be empty (e.g. tests, internal callers without a
// session); eviction is then a no-op.
func (s *KeyService) ChangePassword(ctx context.Context, userID, sessionID string, oldPassword, newPassword []byte) error {
	record, err := s.store.GetUserKey(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user key: %w", err)
	}
	if record == nil {
		return ErrUserKeysMissing
	}

	// Unwrap with old password
	oldKEK, err := DeriveKEKFromPassword(oldPassword, record.Salt)
	if err != nil {
		return fmt.Errorf("derive old KEK: %w", err)
	}
	defer zeroBytes(oldKEK)
	dek, err := UnwrapDEK(oldKEK, record.WrappedDEK)
	if err != nil {
		// Invalid password: uniform failure code so the handler can
		// map to 403 via errors.Is. We deliberately drop the wrapped
		// AEAD/bcrypt diagnostic so a future log-formatter that
		// prints the error verbatim does not leak the underlying
		// failure mode (validator pass-3 finding NEW-7).
		return ErrInvalidPassword
	}
	defer zeroBytes(dek)

	// Re-wrap with new password
	newSalt, err := GenerateSalt()
	if err != nil {
		return fmt.Errorf("generate new salt: %w", err)
	}
	newKEK, err := DeriveKEKFromPassword(newPassword, newSalt)
	if err != nil {
		return fmt.Errorf("derive new KEK: %w", err)
	}
	defer zeroBytes(newKEK)
	newWrappedDEK, err := WrapDEK(newKEK, dek)
	if err != nil {
		return fmt.Errorf("wrap DEK with new password: %w", err)
	}

	// Evict the cached DEK BEFORE the wrap update commits. Order
	// matters: if we evicted after the commit, a concurrent request
	// from the same JWT landing between the commit and the evict
	// would still get a cache hit and run with the discarded DEK
	// (validator pass-4 finding NEW-4). Pre-commit eviction means
	// the worst case is the user has to re-Unlock with their OLD
	// password — which still works because user_keys.wrapped_dek
	// hasn't changed yet.
	//
	// Cache-evict errors are non-fatal but observable: a Redis
	// outage that silently leaves the cached DEK in place re-opens
	// the race window the reorder closed. Log Warn so operators see
	// the degradation (validator pass-5 finding N-3).
	if sessionID != "" && s.cache != nil {
		if err := s.cache.EvictDEK(ctx, sessionID); err != nil && s.logger != nil {
			s.logger.Warn("ChangePassword: DEK evict failed; cached DEK may be stale until TTL",
				"userID", userID, "sessionID", sessionID, "error", err.Error())
		}
	}

	if err := s.store.UpdateWrappedDEK(ctx, userID, newWrappedDEK, newSalt, record.KeyVersion); err != nil {
		return err
	}
	return nil
}

// ResetWithRecoveryKey unwraps the DEK using the recovery key and re-wraps with a new password.
// Returns a new recovery key (hex-encoded).
func (s *KeyService) ResetWithRecoveryKey(ctx context.Context, userID string, recoveryKeyHex string, newPassword []byte) (newRecoveryKeyHex string, err error) {
	record, err := s.store.GetUserKey(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("get user key: %w", err)
	}
	if record == nil {
		return "", ErrUserKeysMissing
	}
	if record.WrappedDEKRecovery == nil || record.RecoverySalt == nil {
		return "", errors.New("no recovery key configured for this user")
	}

	recoveryKey, err := hex.DecodeString(recoveryKeyHex)
	if err != nil {
		return "", errors.New("invalid recovery key format")
	}

	recoveryKEK, err := DeriveKEKFromKey(recoveryKey, record.RecoverySalt, recInfo)
	if err != nil {
		return "", fmt.Errorf("derive recovery KEK: %w", err)
	}
	defer zeroBytes(recoveryKEK)

	dek, err := UnwrapDEK(recoveryKEK, record.WrappedDEKRecovery)
	if err != nil {
		return "", fmt.Errorf("%w: recovery key did not unwrap", ErrInvalidPassword)
	}
	defer zeroBytes(dek)

	// Re-wrap with new password
	newSalt, err := GenerateSalt()
	if err != nil {
		return "", fmt.Errorf("generate new salt: %w", err)
	}
	newKEK, err := DeriveKEKFromPassword(newPassword, newSalt)
	if err != nil {
		return "", fmt.Errorf("derive new KEK: %w", err)
	}
	defer zeroBytes(newKEK)
	newWrappedDEK, err := WrapDEK(newKEK, dek)
	if err != nil {
		return "", fmt.Errorf("wrap DEK: %w", err)
	}

	if err := s.store.UpdateWrappedDEK(ctx, userID, newWrappedDEK, newSalt, record.KeyVersion); err != nil {
		return "", fmt.Errorf("update wrapped DEK: %w", err)
	}

	// Generate new recovery key
	newRecoveryKey, err := GenerateRecoveryKey()
	if err != nil {
		return "", fmt.Errorf("generate new recovery key: %w", err)
	}
	newRecoverySalt, err := GenerateSalt()
	if err != nil {
		return "", fmt.Errorf("generate new recovery salt: %w", err)
	}
	newRecoveryKEK, err := DeriveKEKFromKey(newRecoveryKey, newRecoverySalt, recInfo)
	if err != nil {
		return "", fmt.Errorf("derive new recovery KEK: %w", err)
	}
	defer zeroBytes(newRecoveryKEK)
	newWrappedDEKRecovery, err := WrapDEK(newRecoveryKEK, dek)
	if err != nil {
		return "", fmt.Errorf("wrap DEK with new recovery: %w", err)
	}

	if err := s.store.UpdateWrappedDEKRecovery(ctx, userID, newWrappedDEKRecovery, newRecoverySalt); err != nil {
		return "", fmt.Errorf("update recovery key: %w", err)
	}

	return hex.EncodeToString(newRecoveryKey), nil
}

// HasKeys checks if a user has key material initialized.
func (s *KeyService) HasKeys(ctx context.Context, userID string) (bool, error) {
	record, err := s.store.GetUserKey(ctx, userID)
	if err != nil {
		return false, err
	}
	return record != nil, nil
}

// RotationResult is what RotateKeyWithPassword returns. NewKeyVersion
// is the bumped key_version; NewRecoveryKeyHex is a freshly-issued
// recovery key (the previous one wraps the now-discarded old DEK and
// is invalid after rotation). Callers MUST surface NewRecoveryKeyHex
// to the user once — the API does not store it anywhere recoverable.
type RotationResult struct {
	NewKeyVersion     int
	NewRecoveryKeyHex string
}

// RotateKeyWithPassword rotates the user's DEK and eagerly re-encrypts
// every secret row under the new DEK in a single transaction.
//
// The flow is:
//
//  1. Verify the password by unwrapping the current DEK with the derived KEK.
//  2. Generate a new random DEK.
//  3. Generate a new recovery key + salt; the old recovery key wraps
//     the old (about-to-be-discarded) DEK and would be useless after
//     rotation. Without this, ResetWithRecoveryKey post-rotate would
//     unwrap a DEK that no longer matches user_secrets.
//  4. Walk all user_secrets rows; decrypt each with the old DEK and
//     re-encrypt with the new DEK. The store implementation runs this
//     under a single atomic operation so partial failures cannot leave
//     orphaned rows.
//  5. Wrap the new DEK with the same KEK and bump key_version,
//     INSIDE the same tx (commit closure). Wrap newDEK with the new
//     recoveryKEK and update user_keys.wrapped_dek_recovery in the
//     same tx.
//  6. Refresh the session DEK cache.
//
// If any step in 4 or 5 fails, the entire tx rolls back: secrets stay
// at the old key_version, user_keys keeps the old wrapped DEK, the
// old recovery key still works. The rotation is a no-op from the
// client's perspective (modulo the function's error return).
//
// SetSecretStore must be called before RotateKeyWithPassword; otherwise
// the function refuses to run.
func (s *KeyService) RotateKeyWithPassword(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration) (RotationResult, error) {
	if s.secretStore == nil {
		return RotationResult{}, errors.New("rotate-key not configured: secret store missing")
	}

	record, err := s.store.GetUserKey(ctx, userID)
	if err != nil {
		return RotationResult{}, fmt.Errorf("get user key: %w", err)
	}
	if record == nil {
		return RotationResult{}, ErrUserKeysMissing
	}

	kek, err := DeriveKEKFromPassword(password, record.Salt)
	if err != nil {
		return RotationResult{}, fmt.Errorf("derive KEK: %w", err)
	}
	defer zeroBytes(kek)

	oldDEK, err := UnwrapDEK(kek, record.WrappedDEK)
	if err != nil {
		return RotationResult{}, ErrInvalidPassword
	}
	defer zeroBytes(oldDEK)

	newDEK, err := GenerateDEK()
	if err != nil {
		return RotationResult{}, fmt.Errorf("generate new DEK: %w", err)
	}
	defer zeroBytes(newDEK)

	newVersion := record.KeyVersion + 1

	newWrappedDEK, err := WrapDEK(kek, newDEK)
	if err != nil {
		return RotationResult{}, fmt.Errorf("wrap new DEK: %w", err)
	}

	// Generate a fresh recovery key and re-wrap the new DEK with it.
	// The previous recovery key wrapped the OLD DEK; without this
	// step, ResetWithRecoveryKey post-rotation would yield the old
	// DEK and every secret (now encrypted with the new DEK) would be
	// undecryptable — exactly the data-loss class of bug Bug 9 fixed
	// for the password path. Argued as A2 in the worklog 0094 pass-2
	// audit.
	newRecoveryKey, err := GenerateRecoveryKey()
	if err != nil {
		return RotationResult{}, fmt.Errorf("generate new recovery key: %w", err)
	}
	defer zeroBytes(newRecoveryKey)
	newRecoverySalt, err := GenerateSalt()
	if err != nil {
		return RotationResult{}, fmt.Errorf("generate new recovery salt: %w", err)
	}
	newRecoveryKEK, err := DeriveKEKFromKey(newRecoveryKey, newRecoverySalt, recInfo)
	if err != nil {
		return RotationResult{}, fmt.Errorf("derive new recovery KEK: %w", err)
	}
	defer zeroBytes(newRecoveryKEK)
	newWrappedDEKRecovery, err := WrapDEK(newRecoveryKEK, newDEK)
	if err != nil {
		return RotationResult{}, fmt.Errorf("wrap new DEK with recovery KEK: %w", err)
	}

	transform := func(oldCT []byte) ([]byte, error) {
		plaintext, derr := DecryptSecret(oldDEK, oldCT)
		if derr != nil {
			return nil, fmt.Errorf("decrypt with old DEK: %w", derr)
		}
		defer zeroBytes(plaintext)
		newCT, eerr := EncryptSecret(newDEK, plaintext)
		if eerr != nil {
			return nil, fmt.Errorf("encrypt with new DEK: %w", eerr)
		}
		return newCT, nil
	}
	commit := func(txCtx context.Context) error {
		// Both writes run inside the same tx via withTx/txFromContext.
		// If the recovery-wrap update fails, the entire rotation
		// rolls back: secrets stay at old key_version, user_keys
		// stays at old wrapped DEK + old recovery wrap.
		if err := s.store.UpdateWrappedDEK(txCtx, userID, newWrappedDEK, record.Salt, newVersion); err != nil {
			return err
		}
		return s.store.UpdateWrappedDEKRecovery(txCtx, userID, newWrappedDEKRecovery, newRecoverySalt)
	}
	if err := s.secretStore.ReEncryptUserSecrets(ctx, userID, newVersion, transform, commit); err != nil {
		return RotationResult{}, fmt.Errorf("re-encrypt user secrets: %w", err)
	}

	if err := s.cache.CacheDEK(ctx, sessionID, newDEK, ttl); err != nil {
		return RotationResult{}, fmt.Errorf("cache new DEK: %w", err)
	}

	s.rewrapAPIKeyDEKs(ctx, userID, newDEK)

	return RotationResult{
		NewKeyVersion:     newVersion,
		NewRecoveryKeyHex: hex.EncodeToString(newRecoveryKey),
	}, nil
}

func (s *KeyService) rewrapAPIKeyDEKs(ctx context.Context, userID string, newDEK []byte) {
	if s.apiKeyStore == nil || s.rootKeyProvider == nil {
		return
	}

	keys, err := s.apiKeyStore.ListAPIKeysWithDecrypt(ctx, userID)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("rewrapAPIKeyDEKs: failed to list API keys", "userID", userID, "error", err.Error())
		}
		return
	}

	for _, key := range keys {
		if !key.DecryptAccess || len(key.KeyCiphertext) == 0 {
			continue
		}

		rawKey, decErr := s.rootKeyProvider.Decrypt(ctx, key.KeyCiphertext)
		if decErr != nil {
			if s.logger != nil {
				s.logger.Warn("rewrapAPIKeyDEKs: failed to decrypt key_ciphertext",
					"keyID", key.ID, "error", decErr.Error())
			}
			_ = s.apiKeyStore.UpdateAPIKeyDEK(ctx, key.ID, nil, nil, false)
			continue
		}

		apiKEK, deriveErr := DeriveKEKFromKey(rawKey, key.KekSalt, "llmsafespaces-apikey-kek")
		zeroBytes(rawKey)
		if deriveErr != nil {
			if s.logger != nil {
				s.logger.Warn("rewrapAPIKeyDEKs: failed to derive API KEK",
					"keyID", key.ID, "error", deriveErr.Error())
			}
			_ = s.apiKeyStore.UpdateAPIKeyDEK(ctx, key.ID, nil, nil, false)
			continue
		}

		wrappedDEK, wrapErr := EncryptSecret(apiKEK, newDEK)
		zeroBytes(apiKEK)
		if wrapErr != nil {
			if s.logger != nil {
				s.logger.Warn("rewrapAPIKeyDEKs: failed to wrap new DEK",
					"keyID", key.ID, "error", wrapErr.Error())
			}
			_ = s.apiKeyStore.UpdateAPIKeyDEK(ctx, key.ID, nil, nil, false)
			continue
		}

		if updateErr := s.apiKeyStore.UpdateAPIKeyDEK(ctx, key.ID, wrappedDEK, key.KekSalt, true); updateErr != nil {
			if s.logger != nil {
				s.logger.Warn("rewrapAPIKeyDEKs: failed to update wrapped DEK in DB",
					"keyID", key.ID, "error", updateErr.Error())
			}
		}
	}
}

// zeroBytes overwrites b with zeros to reduce the time secret material
// lingers in memory after the function that owned it returns.
//
// The Go specification does NOT formally guarantee that this write
// cannot be eliminated by the compiler. In practice the current Go
// compiler does not elide it (the slice escapes via the caller), and
// the runtime.KeepAlive call below explicitly defeats any future
// elimination by extending b's lifetime past the loop. This is
// best-effort defense-in-depth, not a confidentiality boundary —
// callers must not rely on this for timing-channel resistance, and
// the underlying memory may have been swapped to disk before the wipe
// runs anyway.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}
