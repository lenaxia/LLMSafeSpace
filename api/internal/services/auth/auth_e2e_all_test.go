package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// TestE2E_RealAuth_WorkspaceEnv tests PUT/GET/DELETE /workspaces/:id/env
func TestE2E_RealAuth_WorkspaceEnv(t *testing.T) {
	router, token, _ := setupRealAuthRouter(t)
	ln := startServer(t, router)
	defer ln.Close()
	base := "http://" + ln.Addr().String()
	c := &http.Client{Timeout: 5 * time.Second}

	// Set env vars
	resp := doPut(t, c, base+"/api/v1/workspaces/ws-env-test/env",
		`{"vars":{"DATABASE_URL":"postgres://x","API_KEY":"secret123"}}`, token)
	if resp.StatusCode != 204 {
		t.Fatalf("SetEnv: expected 204, got %d: %s", resp.StatusCode, readAll(t, resp))
	}
	resp.Body.Close()

	// Get env vars (names only, never values)
	resp = doGet(t, c, base+"/api/v1/workspaces/ws-env-test/env", token)
	if resp.StatusCode != 200 {
		t.Fatalf("GetEnv: expected 200, got %d", resp.StatusCode)
	}
	var envResp struct{ Vars []string }
	json.NewDecoder(resp.Body).Decode(&envResp)
	resp.Body.Close()
	if len(envResp.Vars) != 2 {
		t.Errorf("Expected 2 env vars, got %d", len(envResp.Vars))
	}

	// Delete one env var
	req, _ := http.NewRequest("DELETE", base+"/api/v1/workspaces/ws-env-test/env/DATABASE_URL", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ = c.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("DeleteEnv: expected 204, got %d: %s", resp.StatusCode, readAll(t, resp))
	}
	resp.Body.Close()

	// Verify only 1 remains
	resp = doGet(t, c, base+"/api/v1/workspaces/ws-env-test/env", token)
	json.NewDecoder(resp.Body).Decode(&envResp)
	resp.Body.Close()
	if len(envResp.Vars) != 1 {
		t.Errorf("Expected 1 env var after delete, got %d", len(envResp.Vars))
	}

	t.Log("E2E WorkspaceEnv: PUT/GET/DELETE — PASSED")
}

// TestE2E_RealAuth_ChangePassword tests POST /account/change-password
func TestE2E_RealAuth_ChangePassword(t *testing.T) {
	router, token, svc := setupRealAuthRouter(t)
	ln := startServer(t, router)
	defer ln.Close()
	base := "http://" + ln.Addr().String()
	c := &http.Client{Timeout: 5 * time.Second}

	// Change password
	resp := doPost(t, c, base+"/api/v1/account/change-password",
		`{"oldPassword":"secure-password-123","newPassword":"new-secure-password-456"}`, token)
	if resp.StatusCode != 204 {
		t.Fatalf("ChangePassword: expected 204, got %d: %s", resp.StatusCode, readAll(t, resp))
	}
	resp.Body.Close()

	// Login with old password should fail
	resp = doPost(t, c, base+"/api/v1/auth/login",
		`{"email":"test@example.com","password":"secure-password-123"}`, "")
	if resp.StatusCode != 401 {
		t.Errorf("Old password login: expected 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Login with new password should succeed
	resp = doPost(t, c, base+"/api/v1/auth/login",
		`{"email":"test@example.com","password":"new-secure-password-456"}`, "")
	if resp.StatusCode != 200 {
		t.Fatalf("New password login: expected 200, got %d: %s", resp.StatusCode, readAll(t, resp))
	}
	var loginResp struct{ Token string }
	json.NewDecoder(resp.Body).Decode(&loginResp)
	resp.Body.Close()
	newToken := loginResp.Token

	// Secrets should still work with new token
	resp = doPost(t, c, base+"/api/v1/secrets",
		`{"name":"after-pw-change","type":"api-key","value":"sk-test","metadata":{"provider":"x"}}`, newToken)
	if resp.StatusCode != 201 {
		t.Fatalf("Create after pw change: expected 201, got %d: %s", resp.StatusCode, readAll(t, resp))
	}
	resp.Body.Close()

	_ = svc // suppress unused
	t.Log("E2E ChangePassword: change → old fails → new works → secrets work — PASSED")
}

// TestE2E_RealAuth_ChangePassword_WrongOld tests wrong old password
func TestE2E_RealAuth_ChangePassword_WrongOld(t *testing.T) {
	router, token, _ := setupRealAuthRouter(t)
	ln := startServer(t, router)
	defer ln.Close()
	base := "http://" + ln.Addr().String()
	c := &http.Client{Timeout: 5 * time.Second}

	resp := doPost(t, c, base+"/api/v1/account/change-password",
		`{"oldPassword":"wrong-password","newPassword":"doesnt-matter"}`, token)
	if resp.StatusCode != 403 {
		t.Fatalf("Wrong old password: expected 403, got %d: %s", resp.StatusCode, readAll(t, resp))
	}
	resp.Body.Close()
	t.Log("E2E ChangePassword wrong old: 403 — PASSED")
}

// TestE2E_RealAuth_Recover tests POST /account/recover
func TestE2E_RealAuth_Recover(t *testing.T) {
	router, _, svc := setupRealAuthRouter(t)
	ln := startServer(t, router)
	defer ln.Close()
	base := "http://" + ln.Addr().String()
	c := &http.Client{Timeout: 5 * time.Second}

	// Get the recovery key (stored during registration in the key store)
	// We need to access it from the test setup — it's returned by InitializeUserKeys
	// but not exposed via API. For this test, we'll use the userID from setup.
	userID := svc.testUserID
	recoveryKey := svc.testRecoveryKey

	if recoveryKey == "" {
		t.Skip("Recovery key not captured during setup")
	}

	// Recover with recovery key
	body := fmt.Sprintf(`{"userId":"%s","recoveryKey":"%s","newPassword":"recovered-password-789"}`, userID, recoveryKey)
	resp := doPost(t, c, base+"/api/v1/account/recover", body, "")
	if resp.StatusCode != 200 {
		t.Fatalf("Recover: expected 200, got %d: %s", resp.StatusCode, readAll(t, resp))
	}
	var recoverResp struct {
		RecoveryKey string `json:"recoveryKey"`
	}
	json.NewDecoder(resp.Body).Decode(&recoverResp)
	resp.Body.Close()
	if recoverResp.RecoveryKey == "" {
		t.Error("Should return new recovery key")
	}

	// Login with recovered password
	resp = doPost(t, c, base+"/api/v1/auth/login",
		`{"email":"test@example.com","password":"recovered-password-789"}`, "")
	if resp.StatusCode != 200 {
		t.Fatalf("Login after recovery: expected 200, got %d: %s", resp.StatusCode, readAll(t, resp))
	}
	resp.Body.Close()

	// Old recovery key should no longer work
	resp = doPost(t, c, base+"/api/v1/account/recover", body, "")
	if resp.StatusCode != 403 {
		t.Errorf("Old recovery key: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	t.Log("E2E Recover: reset → login → old key invalid — PASSED")
}

// TestE2E_RealAuth_RotateKey_ThenSecrets tests rotation doesn't break existing secrets
func TestE2E_RealAuth_RotateKey_ThenSecrets(t *testing.T) {
	router, token, _ := setupRealAuthRouter(t)
	ln := startServer(t, router)
	defer ln.Close()
	base := "http://" + ln.Addr().String()
	c := &http.Client{Timeout: 5 * time.Second}

	// Create a secret before rotation
	resp := doPost(t, c, base+"/api/v1/secrets",
		`{"name":"pre-rotate","type":"api-key","value":"sk-before","metadata":{"provider":"x"}}`, token)
	if resp.StatusCode != 201 {
		t.Fatalf("Create pre-rotate: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Rotate
	resp = doPost(t, c, base+"/api/v1/account/rotate-key",
		`{"password":"secure-password-123"}`, token)
	if resp.StatusCode != 200 {
		t.Fatalf("Rotate: %d: %s", resp.StatusCode, readAll(t, resp))
	}
	resp.Body.Close()

	// Create a secret after rotation (uses new DEK)
	resp = doPost(t, c, base+"/api/v1/secrets",
		`{"name":"post-rotate","type":"api-key","value":"sk-after","metadata":{"provider":"y"}}`, token)
	if resp.StatusCode != 201 {
		t.Fatalf("Create post-rotate: expected 201, got %d: %s", resp.StatusCode, readAll(t, resp))
	}
	resp.Body.Close()

	// List should show both
	resp = doGet(t, c, base+"/api/v1/secrets", token)
	var listResp struct{ Secrets []struct{ Name string } }
	json.NewDecoder(resp.Body).Decode(&listResp)
	resp.Body.Close()
	if len(listResp.Secrets) != 2 {
		t.Errorf("Expected 2 secrets, got %d", len(listResp.Secrets))
	}

	t.Log("E2E RotateKey then secrets: create before + after rotation — PASSED")
}

// === Shared setup ===

type testContext struct {
	testUserID      string
	testRecoveryKey string
}

func setupRealAuthRouter(t *testing.T) (*gin.Engine, string, *testContext) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := testConfig()
	log := testLogger()
	db := &fullMockDB{users: make(map[string]*types.User)}
	cache := &mockCache{}

	authSvc, _ := New(cfg, log, db, cache)

	keyStore := &memKeyStore{records: make(map[string]*secrets.UserKeyRecord)}
	dekCache := &memDEKCache{store: make(map[string][]byte)}
	keySvc := secrets.NewKeyService(keyStore, dekCache)
	secretStore := &memSecretStore{secrets: make(map[string]*secrets.UserSecret), bindings: make(map[string][]string)}
	secretSvc := secrets.NewSecretService(keySvc, secretStore)
	secretsHandler := handlers.NewSecretsHandler(secretSvc)
	rotateHandler := handlers.NewRotateKeyHandler(keySvc)
	rotateHandler.SetPasswordUpdater(&bcryptUpdater{db: db})

	tc := &testContext{}
	authSvc.SetKeyService(&capturingKeyService{inner: keySvc, tc: tc})

	router := gin.New()

	// Public routes
	router.POST("/api/v1/auth/register", func(c *gin.Context) {
		var req types.RegisterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		resp, err := authSvc.Register(c.Request.Context(), req)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		// Capture recovery key for test
		tc.testUserID = resp.User.ID
		if r, _ := keyStore.GetUserKey(c.Request.Context(), resp.User.ID); r != nil {
			// Recovery key was already returned during InitializeUserKeys in Register
			// We need to capture it differently — store it from the key service
		}
		c.JSON(201, resp)
	})
	router.POST("/api/v1/auth/login", func(c *gin.Context) {
		var req types.LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		resp, err := authSvc.Login(c.Request.Context(), req)
		if err != nil {
			c.JSON(401, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, resp)
	})
	router.POST("/api/v1/account/recover", rotateHandler.RecoverAccount)

	// Authenticated routes
	authed := router.Group("/api/v1")
	authed.Use(authSvc.AuthMiddleware())
	authed.POST("/secrets", secretsHandler.CreateSecret)
	authed.GET("/secrets", secretsHandler.ListSecrets)
	authed.DELETE("/secrets/:id", secretsHandler.DeleteSecret)
	authed.PUT("/workspaces/:id/env", secretsHandler.SetWorkspaceEnv)
	authed.GET("/workspaces/:id/env", secretsHandler.GetWorkspaceEnv)
	authed.DELETE("/workspaces/:id/env/:name", secretsHandler.DeleteWorkspaceEnv)
	authed.POST("/account/rotate-key", rotateHandler.RotateKey)
	authed.POST("/account/change-password", rotateHandler.ChangePassword)

	// Register + login to get token
	// We do this programmatically to avoid HTTP overhead in setup
	regResp, err := authSvc.Register(context.Background(), types.RegisterRequest{
		Username: "testuser", Email: "test@example.com", Password: "secure-password-123",
	})
	if err != nil {
		t.Fatalf("Setup register: %v", err)
	}
	tc.testUserID = regResp.User.ID

	loginResp, err := authSvc.Login(context.Background(), types.LoginRequest{
		Email: "test@example.com", Password: "secure-password-123",
	})
	if err != nil {
		t.Fatalf("Setup login: %v", err)
	}

	return router, loginResp.Token, tc
}

func startServer(t *testing.T, router *gin.Engine) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := &http.Server{Handler: router}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln
}

func doPut(t *testing.T, c *http.Client, url, body, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	return resp
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return buf.String()
}

// bcryptUpdater implements PasswordHashUpdater for tests.
type bcryptUpdater struct {
	db *fullMockDB
}

func (u *bcryptUpdater) UpdatePasswordHash(_ context.Context, userID string, newPassword []byte) error {
	user := u.db.users[userID]
	if user == nil {
		return fmt.Errorf("user not found")
	}
	hash, err := bcrypt.GenerateFromPassword(newPassword, 4) // low cost for tests
	if err != nil {
		return err
	}
	user.PasswordHash = string(hash)
	return nil
}

// capturingKeyService wraps a real KeyService and captures the recovery key.
type capturingKeyService struct {
	inner *secrets.KeyService
	tc    *testContext
}

func (c *capturingKeyService) InitializeUserKeys(ctx context.Context, userID string, password []byte) (string, error) {
	recoveryKey, err := c.inner.InitializeUserKeys(ctx, userID, password)
	if err == nil {
		c.tc.testRecoveryKey = recoveryKey
	}
	return recoveryKey, err
}

func (c *capturingKeyService) UnlockDEK(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration) error {
	return c.inner.UnlockDEK(ctx, userID, password, sessionID, ttl)
}

func (c *capturingKeyService) HasKeys(ctx context.Context, userID string) (bool, error) {
	return c.inner.HasKeys(ctx, userID)
}
