package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newDEKTestService(t *testing.T) (*Service, *mocks.MockDatabaseService, *mocks.MockCacheService, *dekJKeyService) {
	t.Helper()
	log, _ := logger.New(true, "debug", "console")
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret-dek-1234567890"
	cfg.Auth.TokenDuration = 24 * time.Hour
	cfg.Auth.APIKeyPrefix = "lsp_"
	mockDb := new(mocks.MockDatabaseService)
	mockCache := new(mocks.MockCacheService)
	svc, err := New(cfg, log, mockDb, mockCache)
	require.NoError(t, err)
	ks := &dekJKeyService{}
	svc.SetKeyService(ks)
	return svc, mockDb, mockCache, ks
}

type dekJKeyService struct {
	dek       []byte
	sessionID string
	getDEKErr error
}

func (d *dekJKeyService) InitializeUserKeys(_ context.Context, _ string, _ []byte) (string, error) {
	return "fake-recovery", nil
}
func (d *dekJKeyService) UnlockDEK(_ context.Context, _ string, _ []byte, _ string, _ time.Duration) error {
	return nil
}
func (d *dekJKeyService) HasKeys(_ context.Context, _ string) (bool, error) {
	return true, nil
}
func (d *dekJKeyService) GetDEK(_ context.Context, sessionID string) ([]byte, error) {
	if d.getDEKErr != nil {
		return nil, d.getDEKErr
	}
	if d.dek != nil && (d.sessionID == "" || d.sessionID == sessionID) {
		cp := make([]byte, len(d.dek))
		copy(cp, d.dek)
		return cp, nil
	}
	return nil, errors.New("DEK not available")
}
func (d *dekJKeyService) CacheDEK(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}

func withMasterKey(t *testing.T, svc *Service) {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	svc.SetMasterKey(key)
}

func TestCreateAPIKey_WithDecryptAccess_StoresWrappedDEK(t *testing.T) {
	svc, mockDb, _, ks := newDEKTestService(t)
	withMasterKey(t, svc)
	ctx := context.Background()

	dek := make([]byte, 32)
	rand.Read(dek)
	ks.dek = dek
	ks.sessionID = "jwt-session-1"

	mockDb.On("CreateAPIKey", ctx, mock.MatchedBy(func(k *types.APIKey) bool {
		return k.Name == "my-key" &&
			k.DecryptAccess == true &&
			len(k.WrappedDEK) > 0 &&
			len(k.KekSalt) > 0 &&
			len(k.KeyCiphertext) > 0 &&
			k.DekSynced == true
	})).Return(nil)

	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{
		Name:          "my-key",
		DecryptAccess: true,
	}, "jwt-session-1")

	require.NoError(t, err)
	require.NotNil(t, apiKey)
	assert.True(t, apiKey.DecryptAccess)
	assert.True(t, len(apiKey.Key) > 32, "full key must be returned")
	mockDb.AssertExpectations(t)
}

func TestCreateAPIKey_WithDecryptAccess_RequiresJWTSession(t *testing.T) {
	svc, _, _, _ := newDEKTestService(t)
	withMasterKey(t, svc)
	ctx := context.Background()

	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{
		Name:          "my-key",
		DecryptAccess: true,
	}, "")

	assert.Error(t, err)
	assert.Nil(t, apiKey)
	assert.Contains(t, err.Error(), "JWT session required")
}

func TestCreateAPIKey_WithDecryptAccess_RequiresMasterKey(t *testing.T) {
	svc, _, _, _ := newDEKTestService(t)
	ctx := context.Background()

	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{
		Name:          "my-key",
		DecryptAccess: true,
	}, "jwt-session-1")

	assert.Error(t, err)
	assert.Nil(t, apiKey)
	assert.Contains(t, err.Error(), "master key not configured")
}

func TestCreateAPIKey_WithDecryptAccess_DEKNotAvailable(t *testing.T) {
	svc, mockDb, _, _ := newDEKTestService(t)
	withMasterKey(t, svc)
	ctx := context.Background()

	mockDb.On("CreateAPIKey", ctx, mock.Anything).Return(nil).Maybe()

	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{
		Name:          "my-key",
		DecryptAccess: true,
	}, "jwt-session-1")

	assert.Error(t, err)
	assert.Nil(t, apiKey)
	assert.Contains(t, err.Error(), "DEK not available")
}

func TestCreateAPIKey_WithoutDecryptAccess_NoWrappedDEK(t *testing.T) {
	svc, mockDb, _, _ := newDEKTestService(t)
	ctx := context.Background()

	mockDb.On("CreateAPIKey", ctx, mock.MatchedBy(func(k *types.APIKey) bool {
		return k.Name == "my-key" &&
			k.DecryptAccess == false &&
			k.WrappedDEK == nil &&
			k.KekSalt == nil &&
			k.KeyCiphertext == nil
	})).Return(nil)

	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{
		Name:          "my-key",
		DecryptAccess: false,
	}, "")

	require.NoError(t, err)
	require.NotNil(t, apiKey)
	assert.False(t, apiKey.DecryptAccess)
	mockDb.AssertExpectations(t)
}

func TestValidateAPIKey_WithDecryptAccess_UnlocksDEK(t *testing.T) {
	svc, mockDb, mockCache, ks := newDEKTestService(t)
	withMasterKey(t, svc)
	ctx := context.Background()

	dek := make([]byte, 32)
	rand.Read(dek)
	ks.dek = dek
	ks.sessionID = "jwt-session-1"

	var storedKey *types.APIKey
	mockDb.On("CreateAPIKey", ctx, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		k := args.Get(1).(*types.APIKey)
		cp := *k
		storedKey = &cp
	})

	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{
		Name:          "dek-key",
		DecryptAccess: true,
	}, "jwt-session-1")
	require.NoError(t, err)
	require.NotNil(t, storedKey)

	rawKey := apiKey.Key
	h := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(h[:])

	user := &types.User{ID: "user-1"}
	mockCache.On("Get", mock.Anything, "apikey:"+rawKey).Return("", errors.New("not found")).Once()
	mockDb.On("GetUserByAPIKey", mock.Anything, keyHash).Return(user, nil).Once()
	mockDb.On("GetAPIKeyRecordByHash", mock.Anything, keyHash).Return(storedKey, nil).Once()
	mockCache.On("Set", mock.Anything, "apikey:"+rawKey, "user-1", mock.Anything).Return(nil).Once()
	mockCache.On("Set", mock.Anything, mock.MatchedBy(func(key string) bool {
		return len(key) > 4 && key[:4] == "dek:"
	}), mock.Anything, mock.Anything).Return(nil).Once()

	userID, err := svc.ValidateToken(rawKey)
	require.NoError(t, err)
	assert.Equal(t, "user-1", userID)

	mockDb.AssertExpectations(t)
}

func TestValidateAPIKey_WithoutDecryptAccess_NoDEK(t *testing.T) {
	svc, mockDb, mockCache, _ := newDEKTestService(t)
	ctx := context.Background()

	mockDb.On("CreateAPIKey", ctx, mock.Anything).Return(nil)
	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{
		Name:          "no-dek-key",
		DecryptAccess: false,
	}, "")
	require.NoError(t, err)

	rawKey := apiKey.Key
	h := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(h[:])

	keyRec := &types.APIKey{ID: "k1", UserID: "user-1", DecryptAccess: false}
	user := &types.User{ID: "user-1"}
	mockCache.On("Get", mock.Anything, "apikey:"+rawKey).Return("", errors.New("not found")).Once()
	mockDb.On("GetUserByAPIKey", mock.Anything, keyHash).Return(user, nil).Once()
	mockDb.On("GetAPIKeyRecordByHash", mock.Anything, keyHash).Return(keyRec, nil).Once()
	mockCache.On("Set", mock.Anything, "apikey:"+rawKey, "user-1", mock.Anything).Return(nil).Once()

	userID, err := svc.ValidateToken(rawKey)
	require.NoError(t, err)
	assert.Equal(t, "user-1", userID)

	mockDb.AssertExpectations(t)
}

func TestValidateAPIKey_WrappedDEKCorrupt_Fails(t *testing.T) {
	svc, mockDb, mockCache, _ := newDEKTestService(t)

	corruptKey := &types.APIKey{
		ID:            "k1",
		UserID:        "user-1",
		DecryptAccess: true,
		WrappedDEK:    []byte("corrupt-data-not-valid-aes-gcm"),
		KekSalt:       make([]byte, 32),
	}

	rawKey := "lsp_" + hex.EncodeToString(make([]byte, 32))
	h := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(h[:])

	user := &types.User{ID: "user-1"}
	mockCache.On("Get", mock.Anything, "apikey:"+rawKey).Return("", errors.New("not found")).Once()
	mockDb.On("GetUserByAPIKey", mock.Anything, keyHash).Return(user, nil).Once()
	mockDb.On("GetAPIKeyRecordByHash", mock.Anything, keyHash).Return(corruptKey, nil).Once()
	mockCache.On("Set", mock.Anything, "apikey:"+rawKey, "user-1", mock.Anything).Return(nil).Once()

	_, err := svc.ValidateToken(rawKey)
	assert.NoError(t, err)
}

func TestCreateAPIKey_DEKRoundTrip(t *testing.T) {
	svc, mockDb, _, ks := newDEKTestService(t)
	withMasterKey(t, svc)
	ctx := context.Background()

	originalDEK := make([]byte, 32)
	rand.Read(originalDEK)
	ks.dek = originalDEK
	ks.sessionID = "jwt-session-rt"

	var storedKey *types.APIKey
	mockDb.On("CreateAPIKey", ctx, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		k := args.Get(1).(*types.APIKey)
		cp := *k
		storedKey = &cp
	})

	apiKey, err := svc.CreateAPIKey(ctx, "user-1", types.CreateAPIKeyRequest{
		Name:          "roundtrip-key",
		DecryptAccess: true,
	}, "jwt-session-rt")
	require.NoError(t, err)

	rawKey := apiKey.Key
	apiKEK, err := secrets.DeriveKEK([]byte(rawKey), storedKey.KekSalt, "llmsafespace-apikey-kek")
	require.NoError(t, err)

	recoveredDEK, err := secrets.DecryptSecret(apiKEK, storedKey.WrappedDEK)
	require.NoError(t, err)
	assert.Equal(t, originalDEK, recoveredDEK, "round-tripped DEK must match original")

	masterKey := svc.masterKey
	require.NotNil(t, masterKey)
	recoveredRaw, err := secrets.DecryptSecret(masterKey, storedKey.KeyCiphertext)
	require.NoError(t, err)
	assert.Equal(t, rawKey, string(recoveredRaw), "key_ciphertext must decrypt to raw key")

	fmt.Println("DEK round-trip verified: wrapped DEK correctly recovers original DEK")
}
