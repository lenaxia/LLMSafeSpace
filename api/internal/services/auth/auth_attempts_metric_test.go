// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// gatherAuthAttemptCount reads the current value of
// llmsafespaces_auth_attempts_total{method,result} from the default registry.
func gatherAuthAttemptCount(t *testing.T, method, result string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != "llmsafespaces_auth_attempts_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			gotMethod, gotResult := "", ""
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "method" {
					gotMethod = lp.GetValue()
				}
				if lp.GetName() == "result" {
					gotResult = lp.GetValue()
				}
			}
			if gotMethod == method && gotResult == result {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// Login on success must increment llmsafespaces_auth_attempts_total
// {method="password",result="success"}. This is the dashboard's
// denominator for Auth Failure Ratio — without it the panel cannot
// compute a ratio even though failures are being recorded.
func TestLogin_RecordsAuthAttemptSuccess(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	hash, _ := bcrypt.GenerateFromPassword([]byte("mypassword"), bcrypt.DefaultCost)
	mockDb.On("GetUserByEmail", ctx, "user@example.com").Return(&types.User{
		ID: "u1", Username: "user", Email: "user@example.com",
		PasswordHash: string(hash),
		Active:       true, EmailVerified: true,
	}, nil)

	before := gatherAuthAttemptCount(t, "password", "success")
	_, err := svc.Login(ctx, types.LoginRequest{
		Email:    "user@example.com",
		Password: "mypassword",
	})
	require.NoError(t, err)
	after := gatherAuthAttemptCount(t, "password", "success")
	assert.Equal(t, before+1, after, "auth_attempts_total{password,success} must increment")
}

// A Login that fails because of a wrong password must increment
// llmsafespaces_auth_attempts_total{method="password",result="failure"}.
// Symmetry with the existing auth_failures_total is the contract: the
// dashboard panel computes failure ratio = failure / (success+failure).
func TestLogin_RecordsAuthAttemptFailureOnWrongPassword(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	hash, _ := bcrypt.GenerateFromPassword([]byte("therightone"), bcrypt.DefaultCost)
	mockDb.On("GetUserByEmail", ctx, "user@example.com").Return(&types.User{
		ID: "u1", Username: "user", Email: "user@example.com",
		PasswordHash: string(hash),
		Active:       true, EmailVerified: true,
	}, nil)

	before := gatherAuthAttemptCount(t, "password", "failure")
	_, err := svc.Login(ctx, types.LoginRequest{
		Email:    "user@example.com",
		Password: "wrong",
	})
	require.Error(t, err)
	after := gatherAuthAttemptCount(t, "password", "failure")
	assert.Equal(t, before+1, after, "auth_attempts_total{password,failure} must increment on wrong password")
}

// User-not-found also counts as a password failure attempt — same
// observable outcome from the API's perspective; the dashboard does
// not differentiate.
func TestLogin_RecordsAuthAttemptFailureOnUserNotFound(t *testing.T) {
	svc, mockDb, _ := newTestService(t)
	ctx := context.Background()

	mockDb.On("GetUserByEmail", ctx, "nobody@example.com").Return(nil, nil)

	before := gatherAuthAttemptCount(t, "password", "failure")
	_, err := svc.Login(ctx, types.LoginRequest{
		Email:    "nobody@example.com",
		Password: "whatever",
	})
	require.Error(t, err)
	after := gatherAuthAttemptCount(t, "password", "failure")
	assert.Equal(t, before+1, after, "auth_attempts_total{password,failure} must increment on user-not-found")
}

// A locked-out attempt must also increment the failure counter so the
// Auth Failure Ratio panel's denominator includes it. The lockout path
// short-circuits before the regular failure paths fire, so it is its
// own carve-out.
func TestLogin_RecordsAuthAttemptFailureOnLockout(t *testing.T) {
	svc, _, mockCache := newLockoutService(t)
	ctx := context.Background()

	mockCache.On("Get", ctx, "lockout:locked-attempt@e.com").Return("3", nil)

	before := gatherAuthAttemptCount(t, "password", "failure")
	_, err := svc.Login(ctx, types.LoginRequest{
		Email:    "locked-attempt@e.com",
		Password: "anything",
	})
	require.Error(t, err)
	after := gatherAuthAttemptCount(t, "password", "failure")
	assert.Equal(t, before+1, after, "auth_attempts_total{password,failure} must increment on lockout-blocked attempt")
}
