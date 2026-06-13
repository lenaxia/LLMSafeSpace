// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// Sentinel errors for org key operations.
var (
	// ErrOrgDEKUnavailable is returned when the org DEK is not in the Redis cache.
	// This happens when no org admin has an active authenticated session.
	// The HTTP layer maps this to 409 Conflict with a guidance message.
	ErrOrgDEKUnavailable = errors.New("org DEK not currently cached — an org admin must be logged in")

	// ErrOrgKeyNotFound is returned when an org_key_members row does not exist.
	ErrOrgKeyNotFound = errors.New("org key member record not found")

	// ErrOrgKeyStale is returned when UnwrapDEK fails on an org_key_members row,
	// indicating the wrapped DEK was created with a different password (stale wrap).
	// Recovery: org DEK rotation by another admin, or remove+re-add this admin.
	ErrOrgKeyStale = errors.New("org key member record is stale — org DEK rotation required")
)

// OrgKEKInfo is the HKDF info string for org KEK derivation.
// MUST differ from kekInfo ("llmsafespace-kek") to prevent the org KEK
// from being identical to the user KEK given the same password+salt.
const OrgKEKInfo = "llmsafespace-org-kek"

// OrgKeyService manages org DEK lifecycle (create, unlock, rewrap, rotate).
// Parallel to KeyService (which handles user DEKs) but purpose-built for the
// per-admin-wrapped org DEK model.
type OrgKeyService struct {
	store     OrgKeyStore
	cache     DEKCache
	credStore OrgCredentialReEncryptor
	logger    pkginterfaces.LoggerInterface
}

type OrgCredentialReEncryptor interface {
	ReEncryptOrgCredentials(ctx context.Context, tx pgx.Tx, orgID string, oldDEK, newDEK []byte) (int, error)
}

func NewOrgKeyService(store OrgKeyStore, cache DEKCache) *OrgKeyService {
	return &OrgKeyService{store: store, cache: cache}
}

func (s *OrgKeyService) SetCredentialStore(crs OrgCredentialReEncryptor) {
	s.credStore = crs
}

// SetLogger installs the logger. Optional; non-fatal failures are silent without it.
func (s *OrgKeyService) SetLogger(l pkginterfaces.LoggerInterface) {
	s.logger = l
}

// OrgCacheKey returns the DEK cache key for an org.
// Convention: "org:<orgID>" — namespaced to prevent collision with session IDs
// (JWT jti = UUID format "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx").
// These two namespaces cannot collide: no UUID starts with "org:".
func OrgCacheKey(orgID string) string { return "org:" + orgID }

// InitializeOrgKeys generates the org DEK and wraps it with the founding admin's KEK.
// adminPassword is the founding admin's plaintext password (available at org-creation
// request time; never stored). Returns the org DEK in memory; the caller must cache it
// under OrgCacheKey(orgID) for the session — this function does not cache it.
func (s *OrgKeyService) InitializeOrgKeys(ctx context.Context, orgID, adminUserID string, adminPassword []byte) ([]byte, error) {
	orgDEK, err := GenerateDEK()
	if err != nil {
		return nil, fmt.Errorf("generate org DEK: %w", err)
	}

	adminSalt, err := s.store.GetUserSalt(ctx, adminUserID)
	if err != nil {
		zeroBytes(orgDEK)
		return nil, fmt.Errorf("get admin salt: %w", err)
	}

	adminKEK, err := DeriveKEK(adminPassword, adminSalt, OrgKEKInfo)
	if err != nil {
		zeroBytes(orgDEK)
		return nil, fmt.Errorf("derive admin KEK: %w", err)
	}
	defer zeroBytes(adminKEK)

	wrappedDEK, err := WrapDEK(adminKEK, orgDEK)
	if err != nil {
		zeroBytes(orgDEK)
		return nil, fmt.Errorf("wrap org DEK: %w", err)
	}

	if err := s.store.UpsertOrgKeyMember(ctx, &OrgKeyMemberRecord{
		OrgID:      orgID,
		UserID:     adminUserID,
		WrappedDEK: wrappedDEK,
		KeyVersion: 1,
	}); err != nil {
		zeroBytes(orgDEK)
		return nil, fmt.Errorf("store org key member: %w", err)
	}

	return orgDEK, nil
}

// UnlockOrgDEK unwraps the org DEK for a single (orgID, userID) using a pre-fetched
// OrgKeyMemberRecord and the user's salt (both provided by the caller to avoid per-org
// DB round-trips). Caches the result under OrgCacheKey(orgID) with the given TTL.
// Returns nil if record is nil (user has no key for this org — not yet an admin).
func (s *OrgKeyService) UnlockOrgDEK(ctx context.Context, record *OrgKeyMemberRecord, userSalt []byte, password []byte, ttl time.Duration) error {
	if record == nil {
		return nil
	}

	adminKEK, err := DeriveKEK(password, userSalt, OrgKEKInfo)
	if err != nil {
		return fmt.Errorf("derive KEK: %w", err)
	}
	defer zeroBytes(adminKEK)

	orgDEK, err := UnwrapDEK(adminKEK, record.WrappedDEK)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("UnlockOrgDEK: stale wrap detected; org DEK rotation required",
				"orgID", record.OrgID, "userID", record.UserID)
		}
		return ErrOrgKeyStale
	}

	if err := s.cache.CacheDEK(ctx, OrgCacheKey(record.OrgID), orgDEK, ttl); err != nil {
		zeroBytes(orgDEK)
		return fmt.Errorf("cache org DEK: %w", err)
	}

	return nil
}

// UnlockAllOrgDEKs fetches all org_key_members rows for userID in a single DB query,
// then calls UnlockOrgDEK for each. userSalt may be nil — this function fetches it
// internally via GetUserSalt to avoid requiring the caller to thread it through.
// Non-fatal per org: a stale wrap or DB error is logged as Warn and skipped.
// Returns nil even if some orgs fail — login must not be blocked by org DEK failures.
func (s *OrgKeyService) UnlockAllOrgDEKs(ctx context.Context, userID string, _ []byte, password []byte, ttl time.Duration) error {
	records, err := s.store.GetOrgKeyMembersForUser(ctx, userID)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("UnlockAllOrgDEKs: DB error fetching org key records",
				"userID", userID, "error", err.Error())
		}
		return nil // non-fatal
	}

	if len(records) == 0 {
		return nil
	}

	// Fetch user salt once for all orgs
	userSalt, err := s.store.GetUserSalt(ctx, userID)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("UnlockAllOrgDEKs: could not get user salt",
				"userID", userID, "error", err.Error())
		}
		return nil // non-fatal
	}

	for _, rec := range records {
		if unlockErr := s.UnlockOrgDEK(ctx, rec, userSalt, password, ttl); unlockErr != nil {
			if s.logger != nil {
				s.logger.Warn("UnlockAllOrgDEKs: failed to unlock org DEK",
					"orgID", rec.OrgID, "userID", userID, "error", unlockErr.Error())
			}
			// Continue to next org — non-fatal per org
		}
	}

	return nil
}

// WrapOrgDEKForNewAdmin retrieves the org DEK from cache and wraps it for a new admin.
// Returns ErrOrgDEKUnavailable if the org DEK is not currently cached
// (no admin is logged in — caller should return 409).
func (s *OrgKeyService) WrapOrgDEKForNewAdmin(ctx context.Context, orgID, newAdminUserID string, newAdminPassword []byte) error {
	orgDEK, err := s.cache.GetDEK(ctx, OrgCacheKey(orgID))
	if err != nil {
		return fmt.Errorf("get org DEK from cache: %w", err)
	}
	if orgDEK == nil {
		return ErrOrgDEKUnavailable
	}

	newAdminSalt, err := s.store.GetUserSalt(ctx, newAdminUserID)
	if err != nil {
		return fmt.Errorf("get new admin salt: %w", err)
	}

	newAdminKEK, err := DeriveKEK(newAdminPassword, newAdminSalt, OrgKEKInfo)
	if err != nil {
		return fmt.Errorf("derive new admin KEK: %w", err)
	}
	defer zeroBytes(newAdminKEK)

	wrappedDEK, err := WrapDEK(newAdminKEK, orgDEK)
	if err != nil {
		return fmt.Errorf("wrap org DEK for new admin: %w", err)
	}

	existing, err := s.store.GetOrgKeyMember(ctx, orgID, newAdminUserID)
	if err != nil {
		return fmt.Errorf("get existing key member: %w", err)
	}

	keyVersion := 1
	if existing != nil {
		keyVersion = existing.KeyVersion + 1
	}

	return s.store.UpsertOrgKeyMember(ctx, &OrgKeyMemberRecord{
		OrgID:      orgID,
		UserID:     newAdminUserID,
		WrappedDEK: wrappedDEK,
		KeyVersion: keyVersion,
	})
}

// RewrapOrgDEKForAdmin re-wraps the org DEK for a single admin with a new KEK derived
// from the new password.
//
// CONTRACT: Reads the org DEK from cache only. Returns ErrOrgDEKUnavailable if the
// org DEK is not in cache — there is NO fallback using the old password, because the
// old password is not available in the password-change flow. The caller must handle
// ErrOrgDEKUnavailable as a non-fatal condition and inform the user to trigger
// RotateOrgDEK via POST /orgs/:id/rotate-key.
func (s *OrgKeyService) RewrapOrgDEKForAdmin(ctx context.Context, orgID, userID string, newPassword []byte) error {
	orgDEK, err := s.cache.GetDEK(ctx, OrgCacheKey(orgID))
	if err != nil {
		return fmt.Errorf("get org DEK from cache: %w", err)
	}
	if orgDEK == nil {
		return ErrOrgDEKUnavailable
	}

	userSalt, err := s.store.GetUserSalt(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user salt: %w", err)
	}

	newKEK, err := DeriveKEK(newPassword, userSalt, OrgKEKInfo)
	if err != nil {
		return fmt.Errorf("derive new KEK: %w", err)
	}
	defer zeroBytes(newKEK)

	wrappedDEK, err := WrapDEK(newKEK, orgDEK)
	if err != nil {
		return fmt.Errorf("wrap org DEK: %w", err)
	}

	existing, err := s.store.GetOrgKeyMember(ctx, orgID, userID)
	if err != nil {
		return fmt.Errorf("get existing key member: %w", err)
	}

	keyVersion := 1
	if existing != nil {
		keyVersion = existing.KeyVersion + 1
	}

	return s.store.UpsertOrgKeyMember(ctx, &OrgKeyMemberRecord{
		OrgID:      orgID,
		UserID:     userID,
		WrappedDEK: wrappedDEK,
		KeyVersion: keyVersion,
	})
}

// RewrapAllOrgDEKsForAdmin re-wraps the org DEK for every org this user admins.
// Uses GetOrgKeyMembersForUser (single batch DB query), then calls RewrapOrgDEKForAdmin
// per org. Non-fatal per org: ErrOrgDEKUnavailable and other errors are logged as Warn
// and skipped. Returns nil always — ChangePassword has already succeeded; callers must
// not receive a failure from this method.
func (s *OrgKeyService) RewrapAllOrgDEKsForAdmin(ctx context.Context, userID string, newPassword []byte) error {
	records, err := s.store.GetOrgKeyMembersForUser(ctx, userID)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("RewrapAllOrgDEKsForAdmin: DB error fetching org key records",
				"userID", userID, "error", err.Error())
		}
		return nil
	}

	for _, rec := range records {
		if rewrapErr := s.RewrapOrgDEKForAdmin(ctx, rec.OrgID, userID, newPassword); rewrapErr != nil {
			if s.logger != nil {
				s.logger.Warn("RewrapAllOrgDEKsForAdmin: failed to rewrap org DEK",
					"orgID", rec.OrgID, "userID", userID, "error", rewrapErr.Error())
			}
			// Continue — non-fatal per org
		}
	}

	return nil
}

// GetOrgDEK returns the cached org DEK. Returns ErrOrgDEKUnavailable if not in cache.
func (s *OrgKeyService) GetOrgDEK(ctx context.Context, orgID string) ([]byte, error) {
	dek, err := s.cache.GetDEK(ctx, OrgCacheKey(orgID))
	if err != nil {
		return nil, fmt.Errorf("get org DEK: %w", err)
	}
	if dek == nil {
		return nil, ErrOrgDEKUnavailable
	}
	return dek, nil
}

func (s *OrgKeyService) RotateOrgDEK(ctx context.Context, orgID, adminUserID string, adminPassword []byte) (int, error) {
	oldDEK, err := s.cache.GetDEK(ctx, OrgCacheKey(orgID))
	if err != nil {
		return 0, fmt.Errorf("get old org DEK: %w", err)
	}
	if oldDEK == nil {
		return 0, ErrOrgDEKUnavailable
	}

	newDEK, err := GenerateDEK()
	if err != nil {
		return 0, fmt.Errorf("generate new org DEK: %w", err)
	}

	adminSalt, err := s.store.GetUserSalt(ctx, adminUserID)
	if err != nil {
		zeroBytes(newDEK)
		return 0, fmt.Errorf("get admin salt: %w", err)
	}

	adminKEK, err := DeriveKEK(adminPassword, adminSalt, OrgKEKInfo)
	if err != nil {
		zeroBytes(newDEK)
		return 0, fmt.Errorf("derive admin KEK: %w", err)
	}
	defer zeroBytes(adminKEK)

	wrappedNewDEK, err := WrapDEK(adminKEK, newDEK)
	if err != nil {
		zeroBytes(newDEK)
		return 0, fmt.Errorf("wrap new org DEK: %w", err)
	}

	tx, err := s.store.BeginTx(ctx)
	if err != nil {
		zeroBytes(newDEK)
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	var reencrypted int
	if s.credStore != nil {
		reencrypted, err = s.credStore.ReEncryptOrgCredentials(ctx, tx, orgID, oldDEK, newDEK)
		if err != nil {
			zeroBytes(newDEK)
			return 0, fmt.Errorf("re-encrypt org credentials: %w", err)
		}
	}

	if err := s.store.DeleteAllOrgKeyMembersTx(ctx, tx, orgID); err != nil {
		zeroBytes(newDEK)
		return 0, fmt.Errorf("delete old key members: %w", err)
	}

	if err := s.store.UpsertOrgKeyMemberTx(ctx, tx, &OrgKeyMemberRecord{
		OrgID:      orgID,
		UserID:     adminUserID,
		WrappedDEK: wrappedNewDEK,
		KeyVersion: 1,
	}); err != nil {
		zeroBytes(newDEK)
		return 0, fmt.Errorf("insert rotating admin key member: %w", err)
	}

	if err := s.store.SetPendingKeyWrapForOtherAdminsTx(ctx, tx, orgID, adminUserID); err != nil {
		zeroBytes(newDEK)
		return 0, fmt.Errorf("set pending for other admins: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		zeroBytes(newDEK)
		return 0, fmt.Errorf("commit rotation transaction: %w", err)
	}
	committed = true

	if err := s.cache.CacheDEK(ctx, OrgCacheKey(orgID), newDEK, 7*24*time.Hour); err != nil {
		if s.logger != nil {
			s.logger.Warn("RotateOrgDEK: failed to cache new org DEK (non-fatal; will be re-cached on next login)",
				"orgID", orgID, "error", err.Error())
		}
	}

	return reencrypted, nil
}
