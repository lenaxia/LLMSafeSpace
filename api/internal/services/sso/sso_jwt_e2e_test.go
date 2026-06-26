// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package sso

// sso_jwt_e2e_test.go closes the gap where every SSO callback test asserted
// result.Token == "jwt-token-abc" (a constant string from fakeIssuer). No test
// ever proved the SSO callback produces a REAL, parseable, validatable JWT
// with the claims the auth middleware expects (sub, jti, exp, iat).
//
// This test wires a real HS256 JWT issuer (same signing method as
// auth.Service.GenerateToken, auth.go:410) as the SSO TokenIssuer, runs the
// full StartLogin → HandleCallback flow against the real fakeIdP, and parses +
// validates the resulting token. A regression where the SSO service stops
// calling issuer.GenerateToken, or where the issuer produces an unparseable
// token, fails here.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// realJWTIssuer implements TokenIssuer with real HS256 signing (the same
// algorithm auth.Service uses at auth.go:410). The secret is random per test
// so tokens can't leak across runs. Validate() round-trips the token so the
// test can assert the SSO callback's output is a genuine JWT.
type realJWTIssuer struct {
	secret []byte
}

func newRealJWTIssuer(t *testing.T) *realJWTIssuer {
	t.Helper()
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	return &realJWTIssuer{secret: secret}
}

func (r *realJWTIssuer) GenerateToken(userID string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		ID:        base64.RawURLEncoding.EncodeToString([]byte(time.Now().String())),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(r.secret)
}

// validate parses + validates the token against the issuer's secret, returning
// the parsed claims. Fails the test if the token is invalid or unparseable.
func (r *realJWTIssuer) validate(t *testing.T, tokenString string) jwt.RegisteredClaims {
	t.Helper()
	parsed, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return r.secret, nil
	})
	require.NoError(t, err, "SSO-issued token must be a valid, parseable JWT")
	require.True(t, parsed.Valid, "SSO-issued token must pass signature validation")
	claims, ok := parsed.Claims.(*jwt.RegisteredClaims)
	require.True(t, ok, "token claims must be RegisteredClaims")
	return *claims
}

// TestE2E_SSO_Callback_IssuesRealValidatableJWT wires a REAL HS256 JWT issuer
// (not the fakeIssuer returning "jwt-token-abc") into the SSO service and
// asserts the callback produces a token that parses and validates with the
// correct subject. This is the contract every downstream auth middleware call
// depends on.
func TestE2E_SSO_Callback_IssuesRealValidatableJWT(t *testing.T) {
	issuer := newRealJWTIssuer(t)
	svc, _, users, idp, org, cleanup := setupSSOWithIssuer(t, issuer)
	defer cleanup()

	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "jwt-test@acme.com", "name": "JWT Test"}, nil
	}

	// Start login.
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, err := url.Parse(start.AuthURL)
	require.NoError(t, err)
	state := startURL.Query().Get("state")

	// Callback — the IdP redirects with code+state.
	result, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-jwt-test", state, start.Cookie.Value)
	require.NoError(t, err)

	// THE ASSERTION: the token must be a real JWT, not a constant string.
	assert.NotEqual(t, "jwt-token-abc", result.Token,
		"token must be a real JWT, not the fakeIssuer constant — if this fails, the real issuer was not wired")
	assert.NotEmpty(t, result.Token, "token must be non-empty")

	claims := issuer.validate(t, result.Token)
	assert.Equal(t, result.UserID, claims.Subject,
		"JWT subject must match the auto-provisioned user ID")
	assert.NotEmpty(t, claims.ID, "JWT must carry a jti (token ID for revocation)")
	require.NotNil(t, claims.ExpiresAt, "JWT must carry an expiry")
	assert.True(t, claims.ExpiresAt.After(time.Now()), "JWT must not be expired at issuance")
	require.NotNil(t, claims.IssuedAt, "JWT must carry an issued-at timestamp")

	// User + membership must have been created (the SSO side-effect).
	created, ok := users.users["jwt-test@acme.com"]
	require.True(t, ok, "auto-provisioned user must be stored")
	member, err := svc.orgs.GetOrgMember(context.Background(), org.ID, created.ID)
	require.NoError(t, err)
	require.NotNil(t, member)
}

// TestE2E_SSO_Callback_SuspendedUser_Rejected verifies the unhappy path: a
// user marked Suspended in the user store must NOT receive a JWT. This guards
// the sso.go:579-581 gate that prevents suspended users from getting fresh
// sessions via SSO.
func TestE2E_SSO_Callback_SuspendedUser_Rejected(t *testing.T) {
	issuer := newRealJWTIssuer(t)
	svc, _, users, idp, _, cleanup := setupSSOWithIssuer(t, issuer)
	defer cleanup()

	// Pre-seed a suspended user.
	users.users["suspended@acme.com"] = &types.User{
		ID: "susp-1", Email: "suspended@acme.com",
		Status: types.UserStatusSuspended, Role: "user",
	}
	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "suspended@acme.com"}, nil
	}

	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	_, err = svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-susp", startURL.Query().Get("state"), start.Cookie.Value)
	require.ErrorIs(t, err, ErrUserSuspended,
		"suspended user must be rejected at SSO callback — no JWT issued")
}

// TestE2E_SSO_Callback_EmailNotVerified_Rejected verifies the unhappy path
// for the F8 gate: an IdP token without email_verified=true (when the IdP
// includes the claim) must be rejected. This guards sso.go:571-573.
func TestE2E_SSO_Callback_EmailNotVerified_Rejected(t *testing.T) {
	issuer := newRealJWTIssuer(t)
	svc, _, _, idp, _, cleanup := setupSSOWithIssuer(t, issuer)
	defer cleanup()

	// tokenFn returns email_verified=false — the F8 gate must reject.
	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "unverified@acme.com", "email_verified": false}, nil
	}

	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	_, err = svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-unver", startURL.Query().Get("state"), start.Cookie.Value)
	require.Error(t, err, "unverified email must be rejected at SSO callback (F8 gate)")
}

// setupSSOWithIssuer is setupSSO but with a caller-supplied TokenIssuer,
// so tests can inject a real JWT issuer instead of the fakeIssuer.
func setupSSOWithIssuer(t *testing.T, issuer TokenIssuer) (*Service, *fakeOrgStore, *fakeUserStore, *fakeIdP, *types.Organization, func()) {
	t.Helper()
	idp := newFakeIdP(t, "client-xyz")
	orgs := newFakeOrgStore()
	users := newFakeUserStore()
	org := &types.Organization{ID: "org-acme", Name: "Acme", Slug: "acme", Status: types.OrgStatusActive}
	orgs.addOrg(org)

	svc := newTestService(t, orgs, users, issuer, idp.issuer())

	// Seed the encrypted SSO config via the service mutation path so the
	// encryption is the real one used in production (mirrors setupSSO).
	blob, err := svc.EncryptClientSecret(context.Background(), testClientSecret)
	require.NoError(t, err)
	orgs.configs[org.ID] = &types.OrgSSOConfig{
		OrgID:          org.ID,
		DiscoveryURL:   idp.issuer(),
		ClientID:       "client-xyz",
		ClientSecret:   blob,
		ClaimedDomains: []string{"acme.com"},
		AutoProvision:  true,
	}
	return svc, orgs, users, idp, org, func() { idp.close() }
}
