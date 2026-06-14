// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"time"
)

// OrgAwareKeyService wraps KeyService and additionally unlocks org DEKs for
// all orgs the user is an admin of during UnlockDEK (i.e., at login time).
// It satisfies the auth.KeyServiceInterface interface.
type OrgAwareKeyService struct {
	base      *KeyService
	orgKeySvc *OrgKeyService
}

// NewOrgAwareKeyService creates an OrgAwareKeyService that delegates all
// standard key operations to base and additionally calls UnlockAllOrgDEKs
// on UnlockDEK.
func NewOrgAwareKeyService(base *KeyService, orgKeySvc *OrgKeyService) *OrgAwareKeyService {
	return &OrgAwareKeyService{base: base, orgKeySvc: orgKeySvc}
}

func (s *OrgAwareKeyService) InitializeUserKeys(ctx context.Context, userID string, password []byte) (string, error) {
	return s.base.InitializeUserKeys(ctx, userID, password)
}

func (s *OrgAwareKeyService) UnlockDEK(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration) error {
	if err := s.base.UnlockDEK(ctx, userID, password, sessionID, ttl); err != nil {
		return err
	}
	_ = s.orgKeySvc.UnlockAllOrgDEKs(ctx, userID, nil, password, ttl)
	return nil
}

func (s *OrgAwareKeyService) HasKeys(ctx context.Context, userID string) (bool, error) {
	return s.base.HasKeys(ctx, userID)
}

func (s *OrgAwareKeyService) GetDEK(ctx context.Context, sessionID string) ([]byte, error) {
	return s.base.GetDEK(ctx, sessionID)
}

func (s *OrgAwareKeyService) CacheDEK(ctx context.Context, sessionID string, dek []byte, ttl time.Duration) error {
	return s.base.CacheDEK(ctx, sessionID, dek, ttl)
}

// UnlockAllOrgDEKs delegates to the org key service. Non-fatal: returns nil always.
func (s *OrgAwareKeyService) UnlockAllOrgDEKs(ctx context.Context, userID string, userSalt []byte, password []byte, ttl time.Duration) error {
	return s.orgKeySvc.UnlockAllOrgDEKs(ctx, userID, userSalt, password, ttl)
}
