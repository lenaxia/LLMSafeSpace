// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/pkg/secrets"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// --- fakes ---

type fakeOrgStore struct {
	orgBySlug map[string]*types.Organization
	orgs      map[string]*types.Organization
	configs   map[string]*types.OrgSSOConfig
	members   map[string]map[string]*types.OrgMember // orgID -> userID -> member
	mu        sync.Mutex
}

func newFakeOrgStore() *fakeOrgStore {
	return &fakeOrgStore{
		orgBySlug: map[string]*types.Organization{},
		orgs:      map[string]*types.Organization{},
		configs:   map[string]*types.OrgSSOConfig{},
		members:   map[string]map[string]*types.OrgMember{},
	}
}

func (f *fakeOrgStore) addOrg(org *types.Organization) {
	f.orgs[org.ID] = org
	f.orgBySlug[org.Slug] = org
}
func (f *fakeOrgStore) GetOrgBySlug(_ context.Context, slug string) (*types.Organization, error) {
	return f.orgBySlug[strings.ToLower(slug)], nil
}
func (f *fakeOrgStore) GetSSOConfig(_ context.Context, orgID string) (*types.OrgSSOConfig, error) {
	return f.configs[orgID], nil
}
func (f *fakeOrgStore) UpsertSSOConfig(_ context.Context, cfg *types.OrgSSOConfig) error {
	f.configs[cfg.OrgID] = cfg
	return nil
}
func (f *fakeOrgStore) DeleteSSOConfig(_ context.Context, orgID string) error {
	delete(f.configs, orgID)
	return nil
}
func (f *fakeOrgStore) GetOrgMember(_ context.Context, orgID, userID string) (*types.OrgMember, error) {
	if m, ok := f.members[orgID][userID]; ok {
		return m, nil
	}
	return nil, nil
}
func (f *fakeOrgStore) CountOrgAdmins(_ context.Context, orgID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.members[orgID] {
		if m.Role == types.OrgRoleAdmin {
			n++
		}
	}
	return n, nil
}
func (f *fakeOrgStore) AddOrgMember(_ context.Context, orgID, userID string, role types.OrgRole) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.members[orgID] == nil {
		f.members[orgID] = map[string]*types.OrgMember{}
	}
	f.members[orgID][userID] = &types.OrgMember{OrgID: orgID, UserID: userID, Role: role, CreatedAt: time.Now()}
	return nil
}
func (f *fakeOrgStore) UpdateOrgMemberRole(_ context.Context, orgID, userID string, role types.OrgRole) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.members[orgID] == nil || f.members[orgID][userID] == nil {
		return fmt.Errorf("member not found")
	}
	f.members[orgID][userID].Role = role
	return nil
}

type fakeUserStore struct {
	users    map[string]*types.User // email -> user
	byID     map[string]*types.User
	createFn func(*types.User) error
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{users: map[string]*types.User{}, byID: map[string]*types.User{}}
}
func (f *fakeUserStore) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	return f.users[strings.ToLower(email)], nil
}
func (f *fakeUserStore) CreateUser(_ context.Context, u *types.User) error {
	if f.createFn != nil {
		return f.createFn(u)
	}
	f.users[strings.ToLower(u.Email)] = u
	f.byID[u.ID] = u
	return nil
}

type fakeIssuer struct {
	tok string
	err error
}

func (f *fakeIssuer) GenerateToken(userID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.tok, nil
}

// newTestService wires an SSO service with a real static key provider + state key.
func newTestService(t *testing.T, orgs *fakeOrgStore, users *fakeUserStore, issuer TokenIssuer, redirectBase string) *Service {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	kp, err := secrets.NewStaticKeyProvider(key)
	require.NoError(t, err)
	stateKey := []byte("test-state-hmac-key-0123456789ab")
	svc, err := New(orgs, users, ServiceConfig{
		TokenIssuer:     issuer,
		KeyProvider:     kp,
		StateKey:        stateKey,
		TokenTTL:        time.Hour,
		StateTTL:        10 * time.Minute,
		RedirectBaseURL: redirectBase,
		Logger:          nil,
	})
	require.NoError(t, err)
	return svc
}

// --- fake IdP ---

// fakeIdP is a minimal OIDC provider: serves discovery, JWKS, and a token
// endpoint that signs RS256 ID tokens with a generated keypair. It lets the
// SSO service exercise the real oidc.NewProvider → Exchange → Verify path
// without any external network.
type fakeIdP struct {
	t        *testing.T
	server   *httptest.Server
	privKey  *rsa.PrivateKey
	kid      string
	clientID string
	// tokenFn lets a test customize the claims returned for a code exchange.
	tokenFn func(code string) (map[string]any, error)
	// exchangeErr, if set, makes /token return 500.
	exchangeErr error
	// codes records code_verifier values seen for PKCE assertion.
	codes map[string]string
	// challenge records the S256 code_challenge presented at /authorize. When
	// non-empty, /token enforces the PKCE binding (SHA256(verifier) == challenge).
	// Tests that never hit /authorize leave this empty, preserving the legacy
	// non-validating behavior; the dedicated PKCE test drives /authorize to
	// opt into enforcement (F10).
	challenge string
	mu        sync.Mutex
}

func newFakeIdP(t *testing.T, clientID string) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	fp := &fakeIdP{
		t:        t,
		privKey:  key,
		kid:      "test-kid-1",
		clientID: clientID,
		codes:    map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", fp.handleDiscovery)
	mux.HandleFunc("/jwks", fp.handleJWKS)
	mux.HandleFunc("/authorize", fp.handleAuthorize)
	mux.HandleFunc("/token", fp.handleToken)
	fp.server = httptest.NewServer(mux)
	return fp
}

func (f *fakeIdP) close()         { f.server.Close() }
func (f *fakeIdP) issuer() string { return f.server.URL }

func (f *fakeIdP) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                f.server.URL,
		"authorization_endpoint":                f.server.URL + "/authorize",
		"token_endpoint":                        f.server.URL + "/token",
		"jwks_uri":                              f.server.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"claims_supported":                      []string{"sub", "email", "name", "groups"},
	})
}

func (f *fakeIdP) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	n := base64.RawURLEncoding.EncodeToString(f.privKey.N.Bytes())
	eBuf := big.NewInt(int64(f.privKey.E)).Bytes()
	e := base64.RawURLEncoding.EncodeToString(eBuf)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]any{
			{"kty": "RSA", "kid": f.kid, "use": "sig", "alg": "RS256", "n": n, "e": e},
		},
	})
}

// handleAuthorize records the S256 code_challenge so /token can enforce the
// PKCE binding (F10). A real IdP issues an authorization code here and stores
// it alongside the challenge; this fake simply records the challenge and
// redirects to the redirect_uri with a placeholder code so a test that follows
// the real StartLogin → browser-redirect → /token path exercises the binding.
func (f *fakeIdP) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.challenge = r.URL.Query().Get("code_challenge")
	f.mu.Unlock()
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")
	if redirectURI == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}
	target := redirectURI + "?code=" + url.QueryEscape("issued-code") + "&state=" + url.QueryEscape(state)
	http.Redirect(w, r, target, http.StatusFound)
}

func (f *fakeIdP) handleToken(w http.ResponseWriter, r *http.Request) {
	if f.exchangeErr != nil {
		http.Error(w, f.exchangeErr.Error(), http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")
	verifier := r.FormValue("code_verifier")
	f.mu.Lock()
	f.codes[code] = verifier
	recordedChallenge := f.challenge
	f.mu.Unlock()
	// F10: when /authorize recorded a challenge, /token MUST validate the PKCE
	// binding. A wrong/empty verifier yields 400, exactly as a spec-compliant
	// IdP would. No recorded challenge ⇒ skip (preserves non-validating tests).
	if recordedChallenge != "" {
		if verifier == "" || codeChallenge(verifier) != recordedChallenge {
			http.Error(w, "invalid pkce code_verifier", http.StatusBadRequest)
			return
		}
	}
	claims, err := f.tokenFn(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idToken, err := f.signIDToken(claims)
	if err != nil {
		http.Error(w, "signing failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "fake-access",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	})
}

func (f *fakeIdP) signIDToken(claims map[string]any) (string, error) {
	now := time.Now()
	mapClaims := jwt.MapClaims{
		"iss": f.server.URL,
		"sub": "idp-sub-123",
		"aud": f.clientID,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	}
	for k, v := range claims {
		mapClaims[k] = v
	}
	// Default email_verified=true for a well-configured IdP. Individual tests
	// override this (email_verified=false) to exercise the unverified-email
	// rejection path (F8). Absent ⇒ verified matches the OIDC happy path most
	// real IdPs present; production code still REQUIRES the claim to be true.
	if _, ok := mapClaims["email_verified"]; !ok {
		if _, hasEmail := mapClaims["email"]; hasEmail {
			mapClaims["email_verified"] = true
		}
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, mapClaims)
	tok.Header["kid"] = f.kid
	return tok.SignedString(f.privKey)
}

// --- tests ---

const testClientSecret = "super-secret-value"

func setupSSO(t *testing.T, autoProvision bool, groupMapping map[string]types.OrgRole) (*Service, *fakeOrgStore, *fakeUserStore, *fakeIdP, *types.Organization, func()) {
	t.Helper()
	idp := newFakeIdP(t, "client-xyz")
	orgs := newFakeOrgStore()
	users := newFakeUserStore()
	org := &types.Organization{ID: "org-acme", Name: "Acme", Slug: "acme", Status: types.OrgStatusActive}
	orgs.addOrg(org)

	issuer := &fakeIssuer{tok: "jwt-token-abc"}
	svc := newTestService(t, orgs, users, issuer, idp.issuer())

	// Seed the encrypted SSO config via the service mutation path so the
	// encryption is the real one used in production.
	blob, err := svc.EncryptClientSecret(context.Background(), testClientSecret)
	require.NoError(t, err)
	orgs.configs[org.ID] = &types.OrgSSOConfig{
		OrgID:            org.ID,
		DiscoveryURL:     idp.issuer(),
		ClientID:         "client-xyz",
		ClientSecret:     blob,
		ClaimedDomains:   []string{"acme.com"},
		AutoProvision:    autoProvision,
		GroupRoleMapping: groupMapping,
	}
	return svc, orgs, users, idp, org, func() { idp.close() }
}

func TestStartLogin_RedirectsToIdPWithPKCE(t *testing.T) {
	svc, _, _, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()

	res, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	require.NotEmpty(t, res.Cookie.Value)

	u, err := url.Parse(res.AuthURL)
	require.NoError(t, err)
	require.Equal(t, "/authorize", u.Path)
	require.Equal(t, idp.issuer(), u.Scheme+"://"+u.Host)
	q := u.Query()
	require.Equal(t, "client-xyz", q.Get("client_id"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.NotEmpty(t, q.Get("code_challenge"))
	require.NotEmpty(t, q.Get("state"))
	require.Contains(t, q.Get("scope"), "openid")
	require.Contains(t, q.Get("redirect_uri"), idp.issuer()+"/api/v1/auth/sso/acme/callback")
	// state in URL must match state embedded in the signed cookie
	payload, perr := svc.verifyStateCookie(res.Cookie.Value)
	require.NoError(t, perr)
	require.Equal(t, q.Get("state"), payload.State)
	require.NotEmpty(t, payload.Verifier)
	require.Equal(t, "org-acme", payload.OrgID)
}

func TestStartLogin_NoSSOConfig(t *testing.T) {
	orgs := newFakeOrgStore()
	orgs.addOrg(&types.Organization{ID: "o1", Slug: "nocfg", Status: types.OrgStatusActive})
	users := newFakeUserStore()
	svc := newTestService(t, orgs, users, &fakeIssuer{tok: "x"}, "https://api.example.com")

	_, err := svc.StartLogin(context.Background(), "nocfg", "/api/v1/auth/sso/nocfg/callback")
	require.ErrorIs(t, err, ErrSSONotConfigured)
}

func TestStartLogin_UnknownSlug(t *testing.T) {
	svc, _, _, _, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()

	_, err := svc.StartLogin(context.Background(), "does-not-exist", "/x")
	require.ErrorIs(t, err, ErrSSONotConfigured)
}

func TestCallback_AutoProvisionsUserAndIssuesToken_Full(t *testing.T) {
	svc, _, users, idp, org, cleanup := setupSSO(t, true, nil)
	defer cleanup()

	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "alice@acme.com", "name": "Alice"}, nil
	}

	// 1. Start — capture state from the IdP-bound auth URL and the signed cookie.
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, err := url.Parse(start.AuthURL)
	require.NoError(t, err)
	state := startURL.Query().Get("state")

	// 2. Callback — the IdP would redirect with code+state. We supply the
	//    signed cookie (carries verifier + state) and the same state.
	result, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-for-alice", state, start.Cookie.Value)
	require.NoError(t, err)
	require.Equal(t, "jwt-token-abc", result.Token)
	require.True(t, result.CreatedUser)
	require.Equal(t, "alice@acme.com", result.Email)
	require.Equal(t, types.OrgRoleMember, result.Role)

	// User persisted.
	created, ok := users.users["alice@acme.com"]
	require.True(t, ok, "auto-provisioned user must be stored")
	require.NotEmpty(t, created.ID)

	// Membership created as member.
	member, err := svc.orgs.GetOrgMember(context.Background(), org.ID, created.ID)
	require.NoError(t, err)
	require.NotNil(t, member)
	require.Equal(t, types.OrgRoleMember, member.Role)
}

func TestCallback_ExistingUser_NoCreation(t *testing.T) {
	svc, _, users, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()

	users.users["bob@acme.com"] = &types.User{ID: "bob-1", Email: "bob@acme.com", Status: types.UserStatusActive, Role: "user"}
	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "bob@acme.com"}, nil
	}

	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	result, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-bob", startURL.Query().Get("state"), start.Cookie.Value)
	require.NoError(t, err)
	require.False(t, result.CreatedUser)
	require.Equal(t, "bob-1", result.UserID)
}

func TestCallback_AutoProvisionOff_403(t *testing.T) {
	svc, _, users, idp, _, cleanup := setupSSO(t, false, nil)
	defer cleanup()

	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "nobody@acme.com"}, nil
	}

	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	_, err = svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-x", startURL.Query().Get("state"), start.Cookie.Value)
	require.ErrorIs(t, err, ErrAutoProvisionOff)
	_, exists := users.users["nobody@acme.com"]
	require.False(t, exists, "user must not be created when auto-provision is off")
}

func TestCallback_SuspendedUser_403(t *testing.T) {
	svc, _, users, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()

	users.users["suspended@acme.com"] = &types.User{ID: "s-1", Email: "suspended@acme.com", Status: types.UserStatusSuspended}
	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "suspended@acme.com"}, nil
	}

	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	_, err = svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-s", startURL.Query().Get("state"), start.Cookie.Value)
	require.ErrorIs(t, err, ErrUserSuspended)
}

func TestCallback_GroupMappingAppliesAdmin(t *testing.T) {
	mapping := map[string]types.OrgRole{"sso-admins": types.OrgRoleAdmin}
	svc, _, _, idp, org, cleanup := setupSSO(t, true, mapping)
	defer cleanup()

	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "carol@acme.com", "groups": []string{"sso-admins"}}, nil
	}
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	result, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-c", startURL.Query().Get("state"), start.Cookie.Value)
	require.NoError(t, err)
	require.Equal(t, types.OrgRoleAdmin, result.Role)
	member, _ := svc.orgs.GetOrgMember(context.Background(), org.ID, result.UserID)
	require.Equal(t, types.OrgRoleAdmin, member.Role)
}

func TestCallback_GroupMappingDemotesOnReLogin(t *testing.T) {
	mapping := map[string]types.OrgRole{"sso-admins": types.OrgRoleAdmin}
	svc, _, users, idp, org, cleanup := setupSSO(t, true, mapping)
	defer cleanup()

	// Seed an existing admin membership that should be demoted when the user
	// re-logs in WITHOUT the admin group claim. A second admin is present so the
	// last-admin guard does not block the demotion.
	userID := "dave-1"
	users.users["dave@acme.com"] = &types.User{ID: userID, Email: "dave@acme.com", Status: types.UserStatusActive}
	require.NoError(t, svc.orgs.AddOrgMember(context.Background(), org.ID, userID, types.OrgRoleAdmin))
	require.NoError(t, svc.orgs.AddOrgMember(context.Background(), org.ID, "other-admin", types.OrgRoleAdmin))

	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "dave@acme.com", "groups": []string{"developers"}}, nil
	}
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	result, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-d", startURL.Query().Get("state"), start.Cookie.Value)
	require.NoError(t, err)
	require.Equal(t, types.OrgRoleMember, result.Role, "unmapped groups fall back to member")
	member, _ := svc.orgs.GetOrgMember(context.Background(), org.ID, userID)
	require.Equal(t, types.OrgRoleMember, member.Role, "re-login must demote when admin group is gone and other admins remain")
}

func TestCallback_LastAdminNotDemoted_PreventsOrphan(t *testing.T) {
	mapping := map[string]types.OrgRole{"sso-admins": types.OrgRoleAdmin}
	svc, _, users, idp, org, cleanup := setupSSO(t, true, mapping)
	defer cleanup()

	// dave is the SOLE admin. The IdP no longer grants the admin group. The
	// last-admin guard must keep dave as admin rather than orphaning the org.
	userID := "dave-solo"
	users.users["dave@acme.com"] = &types.User{ID: userID, Email: "dave@acme.com", Status: types.UserStatusActive}
	require.NoError(t, svc.orgs.AddOrgMember(context.Background(), org.ID, userID, types.OrgRoleAdmin))

	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "dave@acme.com", "groups": []string{"developers"}}, nil
	}
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	result, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-d2", startURL.Query().Get("state"), start.Cookie.Value)
	require.NoError(t, err)
	require.Equal(t, types.OrgRoleMember, result.Role, "computed role reflects the (lack of) admin group")
	member, _ := svc.orgs.GetOrgMember(context.Background(), org.ID, userID)
	require.Equal(t, types.OrgRoleAdmin, member.Role, "sole admin must NOT be demoted — org must never be orphaned")
}

func TestCallback_StateMismatch_Rejected(t *testing.T) {
	svc, _, _, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()
	idp.tokenFn = func(string) (map[string]any, error) { return map[string]any{"email": "x@acme.com"}, nil }

	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)

	_, err = svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code", "wrong-state", start.Cookie.Value)
	require.ErrorIs(t, err, ErrStateInvalid)
}

func TestCallback_ExpiredState_Rejected(t *testing.T) {
	svc, _, _, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()
	idp.tokenFn = func(string) (map[string]any, error) { return map[string]any{"email": "x@acme.com"}, nil }

	// Issue a cookie, then forge an expired one reusing the signing key path:
	// we lower the service TTL to 1ns so the cookie is already expired.
	svc.stateTTL = -1 * time.Minute
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	_, err = svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code", startURL.Query().Get("state"), start.Cookie.Value)
	require.ErrorIs(t, err, ErrStateExpired)
}

func TestCallback_ForgedCookie_Rejected(t *testing.T) {
	svc, _, _, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()
	idp.tokenFn = func(string) (map[string]any, error) { return map[string]any{"email": "x@acme.com"}, nil }

	_, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code", "anystate", "AAAA.forged")
	require.ErrorIs(t, err, ErrStateInvalid)
}

func TestCallback_OrgSwitch_Rejected(t *testing.T) {
	svc, orgs, _, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()
	idp.tokenFn = func(string) (map[string]any, error) { return map[string]any{"email": "x@acme.com"}, nil }

	// A second org with its own SSO config bound to a different discovery URL.
	orgs.addOrg(&types.Organization{ID: "org-globex", Slug: "globex", Status: types.OrgStatusActive})
	idp2 := newFakeIdP(t, "client-globex")
	defer idp2.close()
	blob, err := svc.EncryptClientSecret(context.Background(), "globex-secret")
	require.NoError(t, err)
	orgs.configs["org-globex"] = &types.OrgSSOConfig{
		OrgID: "org-globex", DiscoveryURL: idp2.issuer(), ClientID: "client-globex",
		ClientSecret: blob, AutoProvision: true,
	}

	// Start SSO for acme, then attempt the callback against globex's endpoint.
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	_, err = svc.HandleCallback(context.Background(), "globex",
		"/api/v1/auth/sso/globex/callback", "code", startURL.Query().Get("state"), start.Cookie.Value)
	require.ErrorIs(t, err, ErrStateInvalid, "cookie bound to org-acme must not be usable on globex callback")
}

func TestNormalizeDomains(t *testing.T) {
	require.Equal(t, []string{"acme.com", "acme.io"}, NormalizeDomains([]string{"@ACME.com", " acme.io ", "acme.com"}))
	require.Equal(t, []string{}, NormalizeDomains(nil))
	require.Equal(t, []string{"a.com"}, NormalizeDomains([]string{"", "@", "a.com"}))
}

func TestResolveRole(t *testing.T) {
	mapping := map[string]types.OrgRole{"admins": types.OrgRoleAdmin, "devs": types.OrgRoleMember}
	require.Equal(t, types.OrgRoleAdmin, resolveRole([]string{"devs", "admins"}, mapping))
	require.Equal(t, types.OrgRoleMember, resolveRole([]string{"devs"}, mapping))
	require.Equal(t, types.OrgRoleMember, resolveRole([]string{"unknown"}, mapping))
	require.Equal(t, types.OrgRoleMember, resolveRole(nil, nil))
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	orgs := newFakeOrgStore()
	users := newFakeUserStore()
	svc := newTestService(t, orgs, users, &fakeIssuer{tok: "t"}, "")

	blob, err := svc.EncryptClientSecret(context.Background(), "the-secret")
	require.NoError(t, err)
	require.NotEqual(t, "the-secret", string(blob))

	plain, err := svc.decryptSecret(context.Background(), blob)
	require.NoError(t, err)
	require.Equal(t, "the-secret", plain)
}

// --- F8: OIDC email_verified enforcement (US-43.10 / D17) ---
//
// Per OIDC spec, the `email` claim MUST NOT be trusted for account-binding
// decisions (auto-provision or login-match) unless `email_verified == true`.
// A misconfigured/permissive IdP (self-hosted Keycloak, some Auth0 tenants)
// that lets a user register victim@example.com without verifying it would
// otherwise let the attacker SSO into the victim's existing account.

// TestCallback_UnverifiedEmail_Rejected proves the email_verified gate fires
// BEFORE any user lookup/creation: an unverified email is refused with
// ErrEmailUnverified regardless of whether the victim account exists.
func TestCallback_UnverifiedEmail_Rejected(t *testing.T) {
	svc, _, users, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()

	// Seed a victim account the unverified email must NOT bind to.
	users.users["victim@acme.com"] = &types.User{ID: "victim-1", Email: "victim@acme.com", Status: types.UserStatusActive}
	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "victim@acme.com", "name": "Attacker", "email_verified": false}, nil
	}

	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	_, err = svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-unverified", startURL.Query().Get("state"), start.Cookie.Value)
	require.ErrorIs(t, err, ErrEmailUnverified, "unverified email must be rejected before account binding")

	// No auto-provisioning side effect.
	_, created := users.users["attacker@acme.com"]
	require.False(t, created, "unverified email must not provision a new account")
}

// TestCallback_VerifiedEmailAccepted confirms the gate does not over-fire:
// an explicitly verified email completes the normal flow.
func TestCallback_VerifiedEmailAccepted(t *testing.T) {
	svc, _, users, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()

	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "verified@acme.com", "name": "V", "email_verified": true}, nil
	}
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	result, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-v", startURL.Query().Get("state"), start.Cookie.Value)
	require.NoError(t, err)
	require.Equal(t, "verified@acme.com", result.Email)
	_, ok := users.users["verified@acme.com"]
	require.True(t, ok, "verified email must provision the account")
}

// --- F9: Azure AD `memberOf` group-claim fallback (US-43.10) ---
//
// Azure AD (a major enterprise IdP) emits group membership via the `memberOf`
// claim instead of `groups`. effectiveGroups merges both so role mapping works
// regardless of which shape the IdP uses. This previously had zero coverage.

func TestCallback_MemberOfGroups_MappedToRole(t *testing.T) {
	mapping := map[string]types.OrgRole{"sso-admins": types.OrgRoleAdmin}
	svc, _, _, idp, org, cleanup := setupSSO(t, true, mapping)
	defer cleanup()

	// Azure-AD-style token: groups absent, memberOf carries the admin group.
	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{
			"email":    "aad-admin@acme.com",
			"name":     "AAD Admin",
			"memberOf": []string{"sso-admins", "some-other-group"},
		}, nil
	}
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	result, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", "code-aad", startURL.Query().Get("state"), start.Cookie.Value)
	require.NoError(t, err)
	require.Equal(t, types.OrgRoleAdmin, result.Role, "memberOf claim must drive the admin role mapping (Azure AD fallback)")
	member, _ := svc.orgs.GetOrgMember(context.Background(), org.ID, result.UserID)
	require.NotNil(t, member)
	require.Equal(t, types.OrgRoleAdmin, member.Role)
}

func TestEffectiveGroups_MergesGroupsAndMemberOf(t *testing.T) {
	// Pure unit test of the merge helper — locks the union behavior.
	require.Equal(t, []string{"a", "b"}, oidcClaims{Groups: []string{"a", "b"}}.effectiveGroups())
	require.Equal(t, []string{"a", "b"}, oidcClaims{MemberOf: []string{"a", "b"}}.effectiveGroups())
	merged := oidcClaims{Groups: []string{"g"}, MemberOf: []string{"m"}}.effectiveGroups()
	require.ElementsMatch(t, []string{"g", "m"}, merged)
}

// --- F10: PKCE verifier is validated against the challenge (US-43.10) ---
//
// The client (sso.go) correctly sends code_challenge at /authorize and
// code_verifier at /token. The fake IdP previously recorded the verifier but
// never validated it, so a regression omitting the verifier would not be
// caught. This test drives the real /authorize → /token binding.

// hitAuthorize performs the browser step the real flow does: GET the auth URL
// so the IdP records the S256 challenge. It returns the authorization code the
// IdP redirected with.
func hitAuthorize(t *testing.T, authURL string) string {
	t.Helper()
	// Do not follow redirects — we only need the /authorize side effect (the
	// IdP records the challenge) and the issued code from the Location header.
	req, err := http.NewRequest(http.MethodGet, authURL, nil)
	require.NoError(t, err)
	transport := &http.Transport{}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from /authorize, got %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	return loc.Query().Get("code")
}

// TestCallback_PKCEBinding_FullFlow proves the end-to-end PKCE binding: the
// verifier derived from the signed state cookie satisfies the challenge the
// IdP recorded at /authorize.
func TestCallback_PKCEBinding_FullFlow(t *testing.T) {
	svc, _, _, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()

	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "pkce@acme.com", "name": "PKCE"}, nil
	}
	start, err := svc.StartLogin(context.Background(), "acme", idp.issuer()+"/api/v1/auth/sso/acme/callback")
	require.NoError(t, err)
	startURL, _ := url.Parse(start.AuthURL)

	// Browser step: hit /authorize so the fake IdP records the challenge.
	issuedCode := hitAuthorize(t, start.AuthURL)

	result, err := svc.HandleCallback(context.Background(), "acme",
		idp.issuer()+"/api/v1/auth/sso/acme/callback", issuedCode, startURL.Query().Get("state"), start.Cookie.Value)
	require.NoError(t, err)
	require.Equal(t, "pkce@acme.com", result.Email)
}

// TestCallback_PKCEBinding_WrongVerifierRejected proves the IdP side of the
// binding: a verifier that does not hash to the recorded challenge is refused
// at /token. This is what catches a client regression that drops or mutates
// the verifier. Probed directly against the fake IdP's /token so the assertion
// does not depend on the client's own verifier generation.
func TestCallback_PKCEBinding_WrongVerifierRejected(t *testing.T) {
	svc, _, _, idp, _, cleanup := setupSSO(t, true, nil)
	defer cleanup()
	_ = svc // no service interaction needed; we exercise the IdP directly.

	// Record a challenge via /authorize (the browser step).
	authURL := idp.issuer() + "/authorize?response_type=code&client_id=client-xyz" +
		"&redirect_uri=" + url.QueryEscape(idp.issuer()+"/cb") +
		"&state=st&code_challenge_method=S256" +
		"&code_challenge=" + url.QueryEscape(codeChallenge("legit-verifier"))
	hitAuthorize(t, authURL)

	// /token with a WRONG verifier → 400 (PKCE binding fails).
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"some-code"},
		"redirect_uri":  {idp.issuer() + "/cb"},
		"client_id":     {"client-xyz"},
		"code_verifier": {"wrong-verifier"},
	}
	resp, err := http.PostForm(idp.issuer()+"/token", form)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"a verifier that does not match the recorded challenge must be rejected (PKCE binding)")
}
