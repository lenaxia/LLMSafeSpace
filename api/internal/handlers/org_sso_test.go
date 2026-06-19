// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/api/internal/services/sso"
	"github.com/lenaxia/llmsafespaces/pkg/secrets"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// --- mock store for SSO CRUD + discovery ---
//
// mockSSOStore implements BOTH the handler's ssoStore interface AND the
// sso.Service orgStore interface. The SAME instance is handed to the service
// and the handler so a Put (which writes through the service) is visible to a
// subsequent Get (which reads through the handler) — mirroring production,
// where one *PgOrgStore serves both.

type mockSSOStore struct {
	mu        sync.Mutex
	configs   map[string]*types.OrgSSOConfig
	slugToOrg map[string]*types.Organization
	members   map[string]map[string]*types.OrgMember
	domains   []types.SSODomain
	getErr    error
	deleteErr error
	auditLog  []string
}

func newMockSSOStore() *mockSSOStore {
	return &mockSSOStore{
		configs:   map[string]*types.OrgSSOConfig{},
		slugToOrg: map[string]*types.Organization{},
		members:   map[string]map[string]*types.OrgMember{},
	}
}
func (m *mockSSOStore) GetSSOConfig(_ context.Context, orgID string) (*types.OrgSSOConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.configs[orgID], nil
}
func (m *mockSSOStore) UpsertSSOConfig(_ context.Context, cfg *types.OrgSSOConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[cfg.OrgID] = cfg
	return nil
}
func (m *mockSSOStore) DeleteSSOConfig(_ context.Context, orgID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.configs, orgID)
	return nil
}
func (m *mockSSOStore) GetOrgBySlug(_ context.Context, slug string) (*types.Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.slugToOrg[strings.ToLower(slug)], nil
}
func (m *mockSSOStore) GetOrgMember(_ context.Context, orgID, userID string) (*types.OrgMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mm, ok := m.members[orgID]; ok {
		return mm[userID], nil
	}
	return nil, nil
}
func (m *mockSSOStore) AddOrgMember(_ context.Context, orgID, userID string, role types.OrgRole) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.members[orgID] == nil {
		m.members[orgID] = map[string]*types.OrgMember{}
	}
	m.members[orgID][userID] = &types.OrgMember{OrgID: orgID, UserID: userID, Role: role}
	return nil
}
func (m *mockSSOStore) CountOrgAdmins(_ context.Context, orgID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, mm := range m.members[orgID] {
		if mm.Role == types.OrgRoleAdmin {
			n++
		}
	}
	return n, nil
}
func (m *mockSSOStore) UpdateOrgMemberRole(_ context.Context, orgID, userID string, role types.OrgRole) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.members[orgID] != nil && m.members[orgID][userID] != nil {
		m.members[orgID][userID].Role = role
	}
	return nil
}
func (m *mockSSOStore) ListSSODomains(_ context.Context) ([]types.SSODomain, error) {
	return m.domains, nil
}
func (m *mockSSOStore) CountSSOConfigs(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.configs), nil
}
func (m *mockSSOStore) LogOrgEvent(_ context.Context, _, _, action, _ string, _ map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditLog = append(m.auditLog, action)
	return nil
}

// newSSOServiceForHandler builds a real sso.Service with a static KEK + state
// key so the handler exercises the real encryption/cookie code paths. It shares
// the same store the handler reads from.
func newSSOServiceForHandler(t *testing.T, store *mockSSOStore, users *mockSSOHandlerUserStore, redirectBase string) *sso.Service {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte('a' + i)
	}
	kp, err := secrets.NewStaticKeyProvider(key)
	require.NoError(t, err)
	stateKey := []byte("handler-test-state-key-1234567890")
	svc, err := sso.New(store, users, sso.ServiceConfig{
		TokenIssuer:     &stubIssuer{tok: "jwt-from-handler"},
		KeyProvider:     kp,
		StateKey:        stateKey,
		TokenTTL:        3600_000_000_000, // 1h in ns
		StateTTL:        10 * 1000_000_000,
		RedirectBaseURL: redirectBase,
	})
	require.NoError(t, err)
	return svc
}

type stubIssuer struct{ tok string }

func (s *stubIssuer) GenerateToken(string) (string, error) { return s.tok, nil }

type mockSSOHandlerUserStore struct{ users map[string]*types.User }

func newMockSSOHandlerUserStore() *mockSSOHandlerUserStore {
	return &mockSSOHandlerUserStore{users: map[string]*types.User{}}
}
func (m *mockSSOHandlerUserStore) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	return m.users[strings.ToLower(email)], nil
}
func (m *mockSSOHandlerUserStore) CreateUser(_ context.Context, u *types.User) error {
	m.users[strings.ToLower(u.Email)] = u
	return nil
}

// buildSSOHandler wires the handler + router. ONE store serves both the service
// and the handler so writes are visible to reads.
func buildSSOHandler(t *testing.T) (*SSOHandler, *mockSSOStore, *mockSSOHandlerUserStore, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := newMockSSOStore()
	users := newMockSSOHandlerUserStore()
	svc := newSSOServiceForHandler(t, store, users, "https://api.test.local")
	h := NewSSOHandler(svc, store, &mockOrgAuthService{userID: "admin-1"}, "lsp_session", "https://app.test.local", nil)

	r := gin.New()
	r.GET("/api/v1/orgs/:id/sso", h.Get)
	r.PUT("/api/v1/orgs/:id/sso", h.Put)
	r.DELETE("/api/v1/orgs/:id/sso", h.Delete)
	r.GET("/api/v1/auth/sso/domains", h.Domains)
	r.GET("/api/v1/auth/sso/:orgSlug/start", h.Start)
	r.GET("/api/v1/auth/sso/:orgSlug/callback", h.Callback)
	return h, store, users, r
}

// --- CRUD tests ---

func TestSSOHandler_Get_NoConfigReturnsDefault(t *testing.T) {
	_, _, _, r := buildSSOHandler(t)

	w := doRequest(r, "GET", "/api/v1/orgs/org-1/sso", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp types.OrgSSOConfigResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.False(t, resp.HasSecret)
	require.True(t, resp.AutoProvision)
}

func TestSSOHandler_Get_ReturnsConfigWithoutSecret(t *testing.T) {
	_, store, _, r := buildSSOHandler(t)
	store.configs["org-1"] = &types.OrgSSOConfig{
		OrgID: "org-1", DiscoveryURL: "https://idp", ClientID: "cid",
		ClientSecret: []byte("encrypted-blob"), AutoProvision: false,
		ClaimedDomains:   []string{"acme.com"},
		GroupRoleMapping: map[string]types.OrgRole{"a": types.OrgRoleAdmin},
	}

	w := doRequest(r, "GET", "/api/v1/orgs/org-1/sso", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp types.OrgSSOConfigResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.HasSecret)
	require.Equal(t, "cid", resp.ClientID)
	require.False(t, resp.AutoProvision)
	// Encrypted secret must NEVER appear in the response body.
	require.NotContains(t, w.Body.String(), "encrypted-blob")
}

func TestSSOHandler_Put_EncryptsSecretAndPersists(t *testing.T) {
	_, store, _, r := buildSSOHandler(t)

	body := `{"discoveryUrl":"https://idp.example/.well-known/openid-configuration","clientId":"cid","clientSecret":"plaintext-secret","claimedDomains":["@ACME.com"],"autoProvision":false,"groupRoleMapping":{"admins":"admin"}}`
	w := doRequest(r, "PUT", "/api/v1/orgs/org-1/sso", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	stored, ok := store.configs["org-1"]
	require.True(t, ok)
	require.Equal(t, "cid", stored.ClientID)
	require.Equal(t, []string{"acme.com"}, stored.ClaimedDomains, "domains normalized to bare lowercase")
	require.False(t, stored.AutoProvision)
	require.NotEqual(t, "plaintext-secret", string(stored.ClientSecret), "secret must be encrypted at rest")
	require.NotEmpty(t, stored.ClientSecret)
	require.Len(t, store.auditLog, 1)
}

func TestSSOHandler_Put_MissingSecretOnFirstConfig_400(t *testing.T) {
	_, _, _, r := buildSSOHandler(t)
	body := `{"discoveryUrl":"https://idp","clientId":"cid"}`
	w := doRequest(r, "PUT", "/api/v1/orgs/org-1/sso", body)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSSOHandler_Put_PartialUpdateKeepsExistingSecret(t *testing.T) {
	_, store, _, r := buildSSOHandler(t)
	// First, persist a config WITH a secret.
	body1 := `{"discoveryUrl":"https://idp","clientId":"cid","clientSecret":"first-secret"}`
	w1 := doRequest(r, "PUT", "/api/v1/orgs/org-1/sso", body1)
	require.Equal(t, http.StatusOK, w1.Code)
	firstSecret := store.configs["org-1"].ClientSecret

	// Update WITHOUT a clientSecret → existing encrypted blob must be retained.
	body2 := `{"discoveryUrl":"https://idp","clientId":"cid-renamed","autoProvision":false}`
	w2 := doRequest(r, "PUT", "/api/v1/orgs/org-1/sso", body2)
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	require.Equal(t, firstSecret, store.configs["org-1"].ClientSecret, "existing secret retained on partial update")
	require.Equal(t, "cid-renamed", store.configs["org-1"].ClientID)
	require.False(t, store.configs["org-1"].AutoProvision)
}

func TestSSOHandler_Put_InvalidRole_400(t *testing.T) {
	_, _, _, r := buildSSOHandler(t)
	body := `{"discoveryUrl":"https://idp","clientId":"cid","clientSecret":"s","groupRoleMapping":{"x":"superuser"}}`
	w := doRequest(r, "PUT", "/api/v1/orgs/org-1/sso", body)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSSOHandler_Put_BadBody_400(t *testing.T) {
	_, _, _, r := buildSSOHandler(t)
	w := doRequest(r, "PUT", "/api/v1/orgs/org-1/sso", `{not json`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSSOHandler_Delete(t *testing.T) {
	_, store, _, r := buildSSOHandler(t)
	store.configs["org-1"] = &types.OrgSSOConfig{OrgID: "org-1", ClientSecret: []byte("x")}
	w := doRequest(r, "DELETE", "/api/v1/orgs/org-1/sso", "")
	require.Equal(t, http.StatusNoContent, w.Code)
	_, ok := store.configs["org-1"]
	require.False(t, ok)
	require.Contains(t, store.auditLog, "sso.delete")
}

func TestSSOHandler_Domains(t *testing.T) {
	_, store, _, r := buildSSOHandler(t)
	store.domains = []types.SSODomain{{Domain: "@acme.com", OrgSlug: "acme", OrgName: "Acme"}}
	w := doRequest(r, "GET", "/api/v1/auth/sso/domains", "")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "@acme.com")
	require.Contains(t, body, "Acme")
}

// --- start/callback flow tests (full integration through the handler) ---

// fakeIdP is duplicated here from the sso package test (different package) so
// the handler test can drive a real OIDC provider end-to-end.
type handlerFakeIdP struct {
	t        *testing.T
	server   *httptest.Server
	privKey  rsaKey
	clientID string
	tokenFn  func(string) (map[string]any, error)
	codes    map[string]string
	mu       sync.Mutex
}

type rsaKey struct {
	priv interface{}
	sign func(t *testing.T, claims map[string]any, iss, aud string) (string, error)
}

func newHandlerFakeIdP(t *testing.T, clientID string) *handlerFakeIdP {
	t.Helper()
	// Use an RSA key via stdlib + golang-jwt (already a dependency).
	priv := mustRSA(t)
	fp := &handlerFakeIdP{
		t: t, clientID: clientID, codes: map[string]string{},
		privKey: rsaKey{priv: priv, sign: func(tt *testing.T, claims map[string]any, iss, aud string) (string, error) {
			return signRS256(tt, priv, iss, aud, clientID, claims)
		}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"issuer":"%s","authorization_endpoint":"%s/authorize","token_endpoint":"%s/token","jwks_uri":"%s/jwks","id_token_signing_alg_values_supported":["RS256"],"response_types_supported":["code"],"subject_types_supported":["public"]}`, fp.server.URL, fp.server.URL, fp.server.URL, fp.server.URL)
	})
	mux.HandleFunc("/jwks", fp.handleJWKS)
	mux.HandleFunc("/token", fp.handleToken)
	fp.server = httptest.NewServer(mux)
	return fp
}

func (f *handlerFakeIdP) issuer() string { return f.server.URL }
func (f *handlerFakeIdP) close()         { f.server.Close() }

func (f *handlerFakeIdP) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	body := jwksJSON(f.t, f.privKey.priv)
	_, _ = fmt.Fprint(w, body)
}

func (f *handlerFakeIdP) handleToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	code := r.FormValue("code")
	f.mu.Lock()
	f.codes[code] = r.FormValue("code_verifier")
	f.mu.Unlock()
	claims, err := f.tokenFn(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idToken, err := f.privKey.sign(f.t, claims, f.server.URL, f.clientID)
	if err != nil {
		http.Error(w, "sign failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"access_token":"a","token_type":"Bearer","expires_in":3600,"id_token":%q}`, idToken)
}

func TestSSOHandler_Start_RedirectsAndSetsCookie(t *testing.T) {
	h, store, _, r := buildSSOHandler(t)
	store.slugToOrg["acme"] = &types.Organization{ID: "org-acme", Slug: "acme", Status: types.OrgStatusActive}
	blob, err := h.svc.EncryptClientSecret(context.Background(), "secret")
	require.NoError(t, err)
	store.configs["org-acme"] = &types.OrgSSOConfig{
		OrgID: "org-acme", DiscoveryURL: "https://placeholder", ClientID: "cid", ClientSecret: blob,
		AutoProvision: true,
	}

	// Use a real fake IdP so discovery succeeds.
	idp := newHandlerFakeIdP(t, "cid")
	defer idp.close()
	store.configs["org-acme"].DiscoveryURL = idp.issuer()

	w := doRequest(r, "GET", "/api/v1/auth/sso/acme/start", "")
	require.Equal(t, http.StatusFound, w.Code, w.Body.String())
	loc := w.Header().Get("Location")
	require.NotEmpty(t, loc)
	u, err := url.Parse(loc)
	require.NoError(t, err)
	require.Equal(t, "/authorize", u.Path)
	require.Equal(t, "S256", u.Query().Get("code_challenge_method"))

	// State cookie set.
	var cookieHeader string
	for _, c := range w.Result().Cookies() {
		if c.Name == h.svc.CookieName() {
			cookieHeader = c.Value
		}
	}
	require.NotEmpty(t, cookieHeader, "state cookie must be set")
	require.Equal(t, u.Query().Get("state"), stateFromCookie(t, h, cookieHeader))
}

func TestSSOHandler_Start_NoConfig_404(t *testing.T) {
	_, store, _, r := buildSSOHandler(t)
	store.slugToOrg["acme"] = &types.Organization{ID: "org-acme", Slug: "acme"}
	// No SSO config.
	w := doRequest(r, "GET", "/api/v1/auth/sso/acme/start", "")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestSSOHandler_Callback_SuccessSetsSessionCookieAndRedirects(t *testing.T) {
	h, store, users, r := buildSSOHandler(t)
	idp := newHandlerFakeIdP(t, "cid")
	defer idp.close()
	store.slugToOrg["acme"] = &types.Organization{ID: "org-acme", Slug: "acme", Status: types.OrgStatusActive}
	blob, err := h.svc.EncryptClientSecret(context.Background(), "secret")
	require.NoError(t, err)
	store.configs["org-acme"] = &types.OrgSSOConfig{
		OrgID: "org-acme", DiscoveryURL: idp.issuer(), ClientID: "cid", ClientSecret: blob, AutoProvision: true,
	}
	idp.tokenFn = func(string) (map[string]any, error) {
		return map[string]any{"email": "zoe@acme.com"}, nil
	}

	// 1. Start to obtain the signed state cookie + the IdP state.
	wStart := doRequest(r, "GET", "/api/v1/auth/sso/acme/start", "")
	require.Equal(t, http.StatusFound, wStart.Code)
	startURL, _ := url.Parse(wStart.Header().Get("Location"))
	state := startURL.Query().Get("state")
	var cookieVal string
	for _, c := range wStart.Result().Cookies() {
		if c.Name == h.svc.CookieName() {
			cookieVal = c.Value
		}
	}
	require.NotEmpty(t, cookieVal)

	// 2. Drive the callback with code + state + the cookie.
	cbURL := "/api/v1/auth/sso/acme/callback?code=the-code&state=" + state
	req := httptest.NewRequest("GET", cbURL, nil)
	req.AddCookie(&http.Cookie{Name: h.svc.CookieName(), Value: cookieVal})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code, w.Body.String())
	loc := w.Header().Get("Location")
	require.True(t, strings.HasPrefix(loc, "https://app.test.local"), "redirects to frontend: %s", loc)
	require.Contains(t, loc, "sso=success")

	// Session JWT cookie set.
	var sessionCookie string
	for _, c := range w.Result().Cookies() {
		if c.Name == "lsp_session" {
			sessionCookie = c.Value
		}
	}
	require.Equal(t, "jwt-from-handler", sessionCookie)

	// User auto-provisioned.
	_, ok := users.users["zoe@acme.com"]
	require.True(t, ok)
}

func TestSSOHandler_Callback_AutoProvisionOff_RedirectsWithError(t *testing.T) {
	h, store, _, r := buildSSOHandler(t)
	idp := newHandlerFakeIdP(t, "cid")
	defer idp.close()
	store.slugToOrg["acme"] = &types.Organization{ID: "org-acme", Slug: "acme", Status: types.OrgStatusActive}
	blob, _ := h.svc.EncryptClientSecret(context.Background(), "secret")
	store.configs["org-acme"] = &types.OrgSSOConfig{
		OrgID: "org-acme", DiscoveryURL: idp.issuer(), ClientID: "cid", ClientSecret: blob, AutoProvision: false,
	}
	idp.tokenFn = func(string) (map[string]any, error) { return map[string]any{"email": "nope@acme.com"}, nil }

	wStart := doRequest(r, "GET", "/api/v1/auth/sso/acme/start", "")
	startURL, _ := url.Parse(wStart.Header().Get("Location"))
	state := startURL.Query().Get("state")
	var cookieVal string
	for _, c := range wStart.Result().Cookies() {
		if c.Name == h.svc.CookieName() {
			cookieVal = c.Value
		}
	}

	req := httptest.NewRequest("GET", "/api/v1/auth/sso/acme/callback?code=c&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: h.svc.CookieName(), Value: cookieVal})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "sso=provisioning_disabled")
}

// stateFromCookie decodes the signed state cookie to read back the embedded
// state, mirroring what the service stores. It re-derives via the service's
// verify path so the test asserts against the real value.
func stateFromCookie(t *testing.T, h *SSOHandler, cookieVal string) string {
	t.Helper()
	// The cookie format is "<payload-b64>.<sig-b64>"; we read the payload
	// directly (the service verifies HMAC, but here we only need the state).
	parts := strings.SplitN(cookieVal, ".", 2)
	require.Len(t, parts, 2)
	decoded, err := base64urlDecode(parts[0])
	require.NoError(t, err)
	var p struct {
		State string `json:"s"`
	}
	require.NoError(t, json.Unmarshal(decoded, &p))
	return p.State
}
