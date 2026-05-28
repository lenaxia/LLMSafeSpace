package secrets

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
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
	store KeyStore
	cache DEKCache
}

// NewKeyService creates a new KeyService.
func NewKeyService(store KeyStore, cache DEKCache) *KeyService {
	return &KeyService{store: store, cache: cache}
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

	kek, err := DeriveKEK(password, salt, kekInfo)
	if err != nil {
		return "", fmt.Errorf("derive KEK: %w", err)
	}

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

	recoveryKEK, err := DeriveKEK(recoveryKey, recoverySalt, recInfo)
	if err != nil {
		return "", fmt.Errorf("derive recovery KEK: %w", err)
	}

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

	kek, err := DeriveKEK(password, record.Salt, kekInfo)
	if err != nil {
		return fmt.Errorf("derive KEK: %w", err)
	}

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
// Requires the old password to unwrap first.
func (s *KeyService) ChangePassword(ctx context.Context, userID string, oldPassword, newPassword []byte) error {
	record, err := s.store.GetUserKey(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user key: %w", err)
	}
	if record == nil {
		return errors.New("no key material found for user")
	}

	// Unwrap with old password
	oldKEK, err := DeriveKEK(oldPassword, record.Salt, kekInfo)
	if err != nil {
		return fmt.Errorf("derive old KEK: %w", err)
	}
	dek, err := UnwrapDEK(oldKEK, record.WrappedDEK)
	if err != nil {
		return fmt.Errorf("unwrap DEK with old password: %w", err)
	}

	// Re-wrap with new password
	newSalt, err := GenerateSalt()
	if err != nil {
		return fmt.Errorf("generate new salt: %w", err)
	}
	newKEK, err := DeriveKEK(newPassword, newSalt, kekInfo)
	if err != nil {
		return fmt.Errorf("derive new KEK: %w", err)
	}
	newWrappedDEK, err := WrapDEK(newKEK, dek)
	if err != nil {
		return fmt.Errorf("wrap DEK with new password: %w", err)
	}

	return s.store.UpdateWrappedDEK(ctx, userID, newWrappedDEK, newSalt, record.KeyVersion)
}

// ResetWithRecoveryKey unwraps the DEK using the recovery key and re-wraps with a new password.
// Returns a new recovery key (hex-encoded).
func (s *KeyService) ResetWithRecoveryKey(ctx context.Context, userID string, recoveryKeyHex string, newPassword []byte) (newRecoveryKeyHex string, err error) {
	record, err := s.store.GetUserKey(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("get user key: %w", err)
	}
	if record == nil {
		return "", errors.New("no key material found for user")
	}
	if record.WrappedDEKRecovery == nil || record.RecoverySalt == nil {
		return "", errors.New("no recovery key configured for this user")
	}

	recoveryKey, err := hex.DecodeString(recoveryKeyHex)
	if err != nil {
		return "", errors.New("invalid recovery key format")
	}

	recoveryKEK, err := DeriveKEK(recoveryKey, record.RecoverySalt, recInfo)
	if err != nil {
		return "", fmt.Errorf("derive recovery KEK: %w", err)
	}

	dek, err := UnwrapDEK(recoveryKEK, record.WrappedDEKRecovery)
	if err != nil {
		return "", errors.New("invalid recovery key")
	}

	// Re-wrap with new password
	newSalt, err := GenerateSalt()
	if err != nil {
		return "", fmt.Errorf("generate new salt: %w", err)
	}
	newKEK, err := DeriveKEK(newPassword, newSalt, kekInfo)
	if err != nil {
		return "", fmt.Errorf("derive new KEK: %w", err)
	}
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
	newRecoveryKEK, err := DeriveKEK(newRecoveryKey, newRecoverySalt, recInfo)
	if err != nil {
		return "", fmt.Errorf("derive new recovery KEK: %w", err)
	}
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

// RotateKey generates a new DEK, wraps it with the current KEK (requires active session),
// and increments the key version. Old secrets are lazily re-encrypted on next access.
// This method requires password confirmation; use RotateKeyWithPassword instead.
func (s *KeyService) RotateKey(ctx context.Context, userID, sessionID string) (newKeyVersion int, err error) {
	return 0, errors.New("RotateKey requires password; use RotateKeyWithPassword instead")
}

// RotateKeyWithPassword generates a new DEK and re-wraps with the password-derived KEK.
// Old DEK is retained (wrapped with new DEK) for lazy migration of old-version secrets.
func (s *KeyService) RotateKeyWithPassword(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration) (newKeyVersion int, err error) {
	record, err := s.store.GetUserKey(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("get user key: %w", err)
	}
	if record == nil {
		return 0, errors.New("no key material found for user")
	}

	// Derive KEK to verify password
	kek, err := DeriveKEK(password, record.Salt, kekInfo)
	if err != nil {
		return 0, fmt.Errorf("derive KEK: %w", err)
	}

	// Verify password by unwrapping current DEK
	_, err = UnwrapDEK(kek, record.WrappedDEK)
	if err != nil {
		return 0, errors.New("invalid password")
	}

	// Generate new DEK
	newDEK, err := GenerateDEK()
	if err != nil {
		return 0, fmt.Errorf("generate new DEK: %w", err)
	}

	// Wrap new DEK with same KEK
	newWrappedDEK, err := WrapDEK(kek, newDEK)
	if err != nil {
		return 0, fmt.Errorf("wrap new DEK: %w", err)
	}

	// Increment version
	newVersion := record.KeyVersion + 1

	// Update stored wrapped DEK
	if err := s.store.UpdateWrappedDEK(ctx, userID, newWrappedDEK, record.Salt, newVersion); err != nil {
		return 0, fmt.Errorf("update wrapped DEK: %w", err)
	}

	// Cache new DEK in session
	if err := s.cache.CacheDEK(ctx, sessionID, newDEK, ttl); err != nil {
		return 0, fmt.Errorf("cache new DEK: %w", err)
	}

	return newVersion, nil
}
