// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type fakePendingStore struct {
	mu        sync.Mutex
	pending   []database.PendingOrgCleanup
	deleted   []string
	activated []string
	listErr   error
	delErr    error
}

func (f *fakePendingStore) ListPendingOrgsOlderThan(_ context.Context, _ time.Duration) ([]database.PendingOrgCleanup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending, f.listErr
}

func (f *fakePendingStore) HardDeleteOrg(_ context.Context, orgID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, orgID)
	return nil
}

func (f *fakePendingStore) UpdateOrgStatus(_ context.Context, orgID string, _ *types.OrgStatus, _ *types.OrgSubscriptionStatus, _ *types.OrgPlan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activated = append(f.activated, orgID)
	return nil
}

func newCleanerWithFakeLookup(store *fakePendingStore, lookup func(ctx context.Context, customerID string) (bool, error)) *PendingOrgCleaner {
	c := NewPendingOrgCleaner(store, nil, &testLogger{}, time.Hour, 7*24*time.Hour)
	c.checkoutCompletedFn = lookup
	return c
}

func TestPendingCleaner_NoCustomer_HardDeletes(t *testing.T) {
	store := &fakePendingStore{
		pending: []database.PendingOrgCleanup{{OrgID: "org-1", Slug: "a"}},
	}
	c := newCleanerWithFakeLookup(store, func(context.Context, string) (bool, error) {
		t.Fatal("checkout lookup must not be called when no customer id")
		return false, nil
	})
	c.runOnce(context.Background())

	if len(store.deleted) != 1 || store.deleted[0] != "org-1" {
		t.Errorf("expected org-1 hard-deleted, got %v", store.deleted)
	}
	if len(store.activated) != 0 {
		t.Errorf("should not activate, got %v", store.activated)
	}
}

func TestPendingCleaner_CheckoutCompleted_Activates(t *testing.T) {
	store := &fakePendingStore{
		pending: []database.PendingOrgCleanup{{OrgID: "org-1", StripeCustomerID: "cus_1"}},
	}
	c := newCleanerWithFakeLookup(store, func(context.Context, string) (bool, error) {
		return true, nil
	})
	c.runOnce(context.Background())

	if len(store.activated) != 1 || store.activated[0] != "org-1" {
		t.Errorf("expected org-1 activated, got %v", store.activated)
	}
	if len(store.deleted) != 0 {
		t.Errorf("should not delete a paid org, got %v", store.deleted)
	}
}

func TestPendingCleaner_CheckoutExpired_HardDeletes(t *testing.T) {
	store := &fakePendingStore{
		pending: []database.PendingOrgCleanup{{OrgID: "org-1", StripeCustomerID: "cus_1"}},
	}
	c := newCleanerWithFakeLookup(store, func(context.Context, string) (bool, error) {
		return false, nil
	})
	c.runOnce(context.Background())

	if len(store.deleted) != 1 || store.deleted[0] != "org-1" {
		t.Errorf("expected org-1 deleted (expired checkout), got %v", store.deleted)
	}
	if len(store.activated) != 0 {
		t.Errorf("should not activate, got %v", store.activated)
	}
}

func TestPendingCleaner_StripeLookupError_SkipsWithoutDeleting(t *testing.T) {
	store := &fakePendingStore{
		pending: []database.PendingOrgCleanup{{OrgID: "org-1", StripeCustomerID: "cus_1"}},
	}
	c := newCleanerWithFakeLookup(store, func(context.Context, string) (bool, error) {
		return false, errors.New("stripe timeout")
	})
	c.runOnce(context.Background())

	if len(store.deleted) != 0 {
		t.Errorf("API failure must NOT delete, got %v", store.deleted)
	}
	if len(store.activated) != 0 {
		t.Errorf("API failure must not activate, got %v", store.activated)
	}
}

func TestPendingCleaner_ListError_NoMutation(t *testing.T) {
	store := &fakePendingStore{listErr: errors.New("db down")}
	c := newCleanerWithFakeLookup(store, nil)
	c.runOnce(context.Background())

	if len(store.deleted) != 0 || len(store.activated) != 0 {
		t.Errorf("on list error no org should be mutated")
	}
}
