// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/utilities"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/settings"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// hashToken derives a stable Redis cache key from a JWT/API key without
// storing the raw secret in Redis. SHA-256 is the standard choice; MD5
// was used historically (see worklog 0028 / M2) but flagged by gosec
// G401/G501. The output length doesn't matter — Redis happily accepts
// 64-char keys.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// KeyServiceInterface abstracts the key service for DEK lifecycle.
type KeyServiceInterface interface {
	InitializeUserKeys(ctx context.Context, userID string, password []byte) (recoveryKeyHex string, err error)
	UnlockDEK(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration) error
	HasKeys(ctx context.Context, userID string) (bool, error)
}

// SetKeyService sets the optional key service for secret management.
func (s *Service) SetKeyService(ks KeyServiceInterface) {
	s.keyService = ks
}

// SetInstanceSettings injects the instance settings service for runtime config reads.
func (s *Service) SetInstanceSettings(svc *settings.InstanceService) {
	s.instanceSettings = svc
}

// lockoutConfig reads lockout configuration from instance settings (if available),
// falling back to static config values.
func (s *Service) lockoutConfig(ctx context.Context) (enabled bool, attempts int, duration time.Duration) {
	enabled = s.config.Auth.LockoutEnabled
	attempts = s.config.Auth.LockoutAttempts
	duration = s.config.Auth.LockoutDuration

	if s.instanceSettings == nil {
		return
	}
	if v, err := s.instanceSettings.GetBool(ctx, "auth.lockoutEnabled"); err == nil {
		enabled = v
	}
	if v, err := s.instanceSettings.GetInt(ctx, "auth.lockoutAttempts"); err == nil && v > 0 {
		attempts = v
	}
	if v, err := s.instanceSettings.GetInt(ctx, "auth.lockoutDurationMinutes"); err == nil && v > 0 {
		duration = time.Duration(v) * time.Minute
	}
	return
}

// Service handles authentication and authorization
type Service struct {
	logger       *logger.Logger
	config       *config.Config
	dbService    interfaces.DatabaseService
	cacheService interfaces.CacheService
	// jwtSecret is the active signing key. New tokens are always
	// signed with this key.
	jwtSecret []byte
	// jwtPreviousSecrets are previous signing keys retained for
	// validation only. Tokens signed with any of these are still
	// accepted (so existing sessions don't get logged out at the
	// moment of key rotation), but only `jwtSecret` is used for
	// new tokens. F1.7.5 (Epic 17): operator-driven key rotation.
	jwtPreviousSecrets [][]byte
	tokenDuration      time.Duration
	keyService         KeyServiceInterface
	instanceSettings   *settings.InstanceService
}

// Start initializes the auth service
func (s *Service) Start() error {
	return nil
}

// Stop cleans up the auth service
func (s *Service) Stop() error {
	return nil
}

func (s *Service) AuthenticateAPIKey(ctx context.Context, apiKey string) (string, error) {
	// Check if API key is cached
	cacheKey := fmt.Sprintf("apikey:%s", apiKey)

	// Try to get from cache first
	cachedStatus, err := s.cacheService.Get(ctx, cacheKey)
	if err == nil && cachedStatus != "" {
		if cachedStatus == "revoked" {
			return "", errors.New("token has been revoked")
		}
		return cachedStatus, nil
	}

	// Get user from database
	user, err := s.dbService.GetUserByAPIKey(ctx, apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to authenticate API key: %w", err)
	}

	if user == nil {
		return "", errors.New("invalid API key")
	}

	// Cache the API key for 15 minutes
	err = s.cacheService.Set(ctx, cacheKey, user.ID, 15*time.Minute)
	if err != nil {
		s.logger.Error("Failed to cache API key", err, "user_id", user.ID)
		// Continue even if caching fails
	}

	return user.ID, nil
}

// Note: The redundant AuthMiddleware method has been removed as it duplicates
// functionality in the middleware package

// New creates a new auth service
func New(cfg *config.Config, log *logger.Logger, dbService interfaces.DatabaseService, cacheService interfaces.CacheService) (*Service, error) {
	if cfg.Auth.JWTSecret == "" {
		return nil, errors.New("JWT secret is required")
	}

	prev := make([][]byte, 0, len(cfg.Auth.JWTPreviousSecrets))
	for _, p := range cfg.Auth.JWTPreviousSecrets {
		if p != "" {
			prev = append(prev, []byte(p))
		}
	}

	return &Service{
		logger:             log,
		config:             cfg,
		dbService:          dbService,
		cacheService:       cacheService,
		jwtSecret:          []byte(cfg.Auth.JWTSecret),
		jwtPreviousSecrets: prev,
		tokenDuration:      cfg.Auth.TokenDuration,
	}, nil
}

// GetUserID gets the user ID from the context
func (s *Service) GetUserID(c *gin.Context) string {
	userID, exists := c.Get("userID")
	if !exists {
		return ""
	}
	return userID.(string)
}

// RevokeToken revokes a JWT token
func (s *Service) RevokeToken(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Parse token (accepts active key or any previous key for F1.7.5)
	parsedToken, err := s.parseTokenAcceptingRotatedKeys(token)

	if err != nil {
		return fmt.Errorf("failed to parse token: %w", err)
	}

	// Get claims
	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		return errors.New("invalid token claims")
	}

	// Get token ID with proper validation
	jti, _ := claims["jti"].(string)
	if jti == "" {
		if sub, ok := claims["sub"].(string); ok && sub != "" {
			jti = sub
		} else {
			return errors.New("token missing valid jti or sub claim")
		}
	}

	// Get expiration time
	exp, ok := claims["exp"].(float64)
	if !ok {
		return errors.New("invalid expiration time in token")
	}

	// Calculate remaining time until expiration
	expTime := time.Unix(int64(exp), 0)
	remainingTime := time.Until(expTime)

	if remainingTime <= 0 {
		return errors.New("token has already expired")
	}

	// G18 (Epic 17): Add token to blacklist under BOTH cache keys so the
	// revocation is visible to:
	//   1. ValidateToken's hash-based cache fast-path (token:<hash(token)>)
	//   2. ValidateToken's jti-based revocation check (token:<jti>)
	// Without writing both, ValidateToken's fast-path would still return the
	// cached userID and revocation would be silently ignored. See worklog 0078
	// and `auth_revocation_test.go` for the regression that locks this in.
	hashKey := "token:" + hashToken(token)
	jtiKey := "token:" + jti
	if err := s.cacheService.Set(ctx, hashKey, "revoked", remainingTime); err != nil {
		return fmt.Errorf("failed to revoke token (hash key): %w", err)
	}
	if err := s.cacheService.Set(ctx, jtiKey, "revoked", remainingTime); err != nil {
		// Best-effort cleanup of the hash key so we don't leak a half-revoked
		// state. If the cleanup itself fails, log it; the hash key has the
		// same TTL as the JWT so it will expire on its own.
		if cleanupErr := s.cacheService.Delete(ctx, hashKey); cleanupErr != nil {
			s.logger.Error("Failed to cleanup hash-key after jti-key revoke failure",
				cleanupErr, "hash_key", hashKey)
		}
		return fmt.Errorf("failed to revoke token (jti key): %w", err)
	}

	return nil
}

// CheckResourceAccess checks if a user has access to a resource
func (s *Service) CheckResourceAccess(userID, resourceType, resourceID, action string) bool {
	// Check resource ownership
	isOwner, err := s.dbService.CheckResourceOwnership(userID, resourceType, resourceID)
	if err != nil {
		s.logger.Error("Failed to check resource ownership", err,
			"user_id", userID,
			"resource_type", resourceType,
			"resource_id", resourceID,
		)
		return false
	}

	if isOwner {
		return true
	}

	// Check RBAC permissions
	hasPermission, err := s.dbService.CheckPermission(userID, resourceType, resourceID, action)
	if err != nil {
		s.logger.Error("Failed to check permission", err,
			"user_id", userID,
			"resource_type", resourceType,
			"resource_id", resourceID,
			"action", action,
		)
		return false
	}

	return hasPermission
}

// GenerateToken generates a JWT token for a user
func (s *Service) GenerateToken(userID string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"jti": uuid.New().String(),
		"exp": time.Now().Add(s.tokenDuration).Unix(),
		"iat": time.Now().Unix(),
	})

	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

// ValidateToken validates a JWT token or API key
func (s *Service) ValidateToken(tokenString string) (string, error) {
	// Check if token is an API key
	if utilities.IsAPIKey(tokenString, s.config.Auth.APIKeyPrefix) {
		return s.validateAPIKey(tokenString)
	}

	// Check if token is cached
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cacheKey := fmt.Sprintf("token:%s", hashToken(tokenString))

	// Try to get from cache first
	if cachedUserID, err := s.cacheService.Get(ctx, cacheKey); err == nil && cachedUserID != "" {
		if cachedUserID == "revoked" {
			return "", errors.New("token has been revoked")
		}
		return cachedUserID, nil
	}

	// Parse token (accepts active key or any previous key for F1.7.5)
	token, err := s.parseTokenAcceptingRotatedKeys(tokenString)

	if err != nil {
		return "", fmt.Errorf("failed to parse token: %w", err)
	}

	// Validate token
	if !token.Valid {
		return "", errors.New("invalid token")
	}

	// Get claims
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid token claims")
	}

	// Get user ID
	userID, ok := claims["sub"].(string)
	if !ok {
		return "", errors.New("invalid user ID in token")
	}

	// G18 (Epic 17): Defense-in-depth revocation check by jti AFTER parsing.
	// RevokeToken stores under both token:<hash> (fast-path above) AND
	// token:<jti> (this check). The jti check protects against eviction of
	// the hash-key cache entry (e.g., Redis memory pressure) — without it,
	// revocation could be silently bypassed under cache pressure.
	if jti, ok := claims["jti"].(string); ok && jti != "" {
		if status, gerr := s.cacheService.Get(ctx, "token:"+jti); gerr == nil && status == "revoked" {
			return "", errors.New("token has been revoked")
		}
	}

	// Get expiration time
	exp, ok := claims["exp"].(float64)
	if !ok {
		return "", errors.New("invalid expiration time in token")
	}

	// Calculate remaining time until expiration
	expTime := time.Unix(int64(exp), 0)
	remainingTime := time.Until(expTime)

	// Cache the token if it's valid
	if remainingTime > 0 {
		// Cache for the remaining time of the token, but not more than 1 hour
		cacheDuration := remainingTime
		if cacheDuration > time.Hour {
			cacheDuration = time.Hour
		}

		err = s.cacheService.Set(ctx, cacheKey, userID, cacheDuration)
		if err != nil {
			s.logger.Error("Failed to cache token", err, "user_id", userID)
			// Continue even if caching fails
		}
	}

	return userID, nil
}

// validateAPIKey validates an API key (internal method)
func (s *Service) validateAPIKey(apiKey string) (string, error) {
	// Check if API key is cached
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cacheKey := fmt.Sprintf("apikey:%s", apiKey)

	// Try to get from cache first
	if cachedUserID, err := s.cacheService.Get(ctx, cacheKey); err == nil && cachedUserID != "" {
		return cachedUserID, nil
	}

	// Get user from database
	user, err := s.dbService.GetUserByAPIKey(ctx, apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to get user by API key: %w", err)
	}

	if user == nil {
		return "", errors.New("invalid API key")
	}

	// Cache the API key for 15 minutes
	err = s.cacheService.Set(ctx, cacheKey, user.ID, 15*time.Minute)
	if err != nil {
		s.logger.Error("Failed to cache API key", err, "user_id", user.ID)
		// Continue even if caching fails
	}

	return user.ID, nil
}

const bcryptCost = 12

func (s *Service) Register(ctx context.Context, req types.RegisterRequest) (*types.AuthResponse, error) {
	existing, err := s.dbService.GetUserByEmail(ctx, req.Email)
	if err != nil {
		s.logger.Error("Register: failed to check existing user", err)
		return nil, errors.New("registration failed")
	}
	if existing != nil {
		s.logger.Warn("Register: duplicate email attempt", "email", req.Email)
		return nil, apierrors.NewConflictError("user", "email", fmt.Errorf("registration failed"))
	}

	// G8 (Epic 17): role assignment is now atomic in CreateUser via
	// the SQL CTE that counts existing users in the same statement
	// as the INSERT. We pass "user" as the desired role; the database
	// promotes to "admin" if and only if the user count is 0 at the
	// moment of insert. This eliminates the count-then-insert race
	// where two concurrent Register() calls could both observe count=0
	// and both end up admin.
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, errors.New("registration failed")
	}

	userID := uuid.New().String()
	user := &types.User{
		ID:           userID,
		Username:     strings.TrimSpace(req.Username),
		Email:        strings.ToLower(strings.TrimSpace(req.Email)),
		PasswordHash: string(hash),
		Active:       true,
		Role:         "user",
	}

	if err := s.dbService.CreateUser(ctx, user); err != nil {
		s.logger.Error("Register: failed to create user", err)
		return nil, errors.New("registration failed")
	}

	// Initialize encryption keys for secret management (Epic 10).
	//
	// Key initialisation MUST succeed: a half-initialized user (row exists,
	// no DEK) cannot perform any secret operation and login itself cannot
	// recover from this state without re-deriving the KEK from the
	// password (which requires `user_keys` to exist). We therefore fail
	// the entire registration when key init fails.
	//
	// We also unlock the DEK in the same call so the JWT issued below is
	// usable for secret operations immediately. Without this, the new user
	// would receive a token whose jti has no DEK in cache and every secret
	// call would return 403 until they re-logged in (Bug 5, worklog 0085).
	var recoveryKey string
	if s.keyService != nil {
		recoveryKey, err = s.keyService.InitializeUserKeys(ctx, userID, []byte(req.Password))
		if err != nil {
			s.logger.Error("Register: failed to initialize user keys", err, "user_id", userID)
			return nil, errors.New("registration failed")
		}
	}

	token, err := s.GenerateToken(userID)
	if err != nil {
		return nil, errors.New("registration failed")
	}

	if s.keyService != nil {
		jti := utilities.ExtractJTI(token)
		if jti == "" {
			s.logger.Error("Register: issued token has empty jti; refusing registration",
				fmt.Errorf("empty jti"), "user_id", userID)
			return nil, errors.New("registration failed")
		}
		if err := s.keyService.UnlockDEK(ctx, userID, []byte(req.Password), jti, s.tokenDuration); err != nil {
			s.logger.Error("Register: failed to unlock DEK", err, "user_id", userID)
			return nil, errors.New("registration failed")
		}
	}

	user.PasswordHash = ""
	return &types.AuthResponse{Token: token, User: *user, RecoveryKey: recoveryKey}, nil
}

// dummyBcryptHash is a real, well-formed bcrypt hash (cost 12) of an
// arbitrary password the system never accepts. We use a real hash
// rather than a hand-rolled string of zeros because the bcrypt
// library validates the hash form (length, version prefix, salt
// charset) BEFORE running the KDF; an invalid hash short-circuits in
// microseconds and re-opens the user-enumeration timing channel
// (validator finding N5 in worklog 0094 pass-2 audit).
//
// This hash has the canonical 60-byte length, a $2a$12$ prefix, and
// 22 bcrypt-base64 salt chars + 31 hash chars. CompareHashAndPassword
// against any password runs the full cost-12 KDF before failing.
const dummyBcryptHash = "$2a$12$7c6XjTynpWE0yY.2/uC1IufZqmLuVCoJSv3MFVWCPBaWVDaPPwXj."

// VerifyPassword checks the supplied password against the stored
// bcrypt hash for userID. Returns nil on match, ErrInvalidPassword on
// any mismatch / not-found / DB error. The error returned is
// uniform — callers must NOT differentiate between "wrong password"
// and "user does not exist" because doing so leaks user-existence
// status (the same reason Login returns the generic "invalid
// credentials" message).
//
// bcrypt.CompareHashAndPassword runs in constant time relative to the
// hash cost, so timing-channel leakage is bounded by the bcrypt cost
// (12 in this codebase) regardless of password length.
func (s *Service) VerifyPassword(ctx context.Context, userID string, password []byte) error {
	user, err := s.dbService.GetUser(ctx, userID)
	if err != nil || user == nil {
		// Run a dummy bcrypt compare so the response time is
		// indistinguishable from the real-user-wrong-password
		// branch. The constant cost prevents user enumeration via
		// timing. Hash is real (60 chars, $2a$12$ prefix) so bcrypt
		// runs the full KDF before failing.
		_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), password)
		return secrets.ErrInvalidPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), password); err != nil {
		return secrets.ErrInvalidPassword
	}
	return nil
}

func (s *Service) Login(ctx context.Context, req types.LoginRequest) (*types.AuthResponse, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))

	lockoutEnabled, lockoutAttempts, _ := s.lockoutConfig(ctx)
	if lockoutEnabled {
		lockoutKey := fmt.Sprintf("lockout:%s", email)
		if countStr, err := s.cacheService.Get(ctx, lockoutKey); err == nil && countStr != "" {
			var count int
			if _, err := fmt.Sscanf(countStr, "%d", &count); err == nil && count >= lockoutAttempts {
				return nil, errors.New("account temporarily locked due to too many failed attempts")
			}
		}
	}

	user, err := s.dbService.GetUserByEmail(ctx, email)
	if err != nil {
		s.logger.Error("Login: db error", err)
		// G27 (Epic 17 worklog 0089 RT-4.10): run a dummy bcrypt
		// compare so a DB error path takes the same observable time
		// as a successful user lookup with wrong password.
		_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(req.Password))
		return nil, errors.New("invalid email or password")
	}
	if user == nil {
		s.recordFailedAttempt(ctx, email)
		// G27: same as VerifyPassword — burn the bcrypt cycles so
		// no-such-user takes ~226ms instead of ~16ms.
		_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(req.Password))
		return nil, errors.New("invalid email or password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		s.recordFailedAttempt(ctx, email)
		return nil, errors.New("invalid email or password")
	}

	if !user.Active {
		s.recordFailedAttempt(ctx, email)
		return nil, errors.New("invalid email or password")
	}

	s.clearFailedAttempts(ctx, email)

	token, err := s.GenerateToken(user.ID)
	if err != nil {
		return nil, errors.New("login failed")
	}

	// Unlock DEK for secret management (Epic 10)
	if s.keyService != nil {
		jti := utilities.ExtractJTI(token)
		if jti != "" {
			// Auto-initialize keys for pre-Epic 10 users on first login
			hasKeys, _ := s.keyService.HasKeys(ctx, user.ID)
			if !hasKeys {
				if _, err := s.keyService.InitializeUserKeys(ctx, user.ID, []byte(req.Password)); err != nil {
					s.logger.Warn("Login: failed to auto-init keys", "user_id", user.ID, "error", err.Error())
				}
			}
			if err := s.keyService.UnlockDEK(ctx, user.ID, []byte(req.Password), jti, s.tokenDuration); err != nil {
				s.logger.Warn("Login: failed to unlock DEK", "user_id", user.ID, "error", err.Error())
			}
		}
	}

	user.PasswordHash = ""
	return &types.AuthResponse{Token: token, User: *user}, nil
}

func (s *Service) recordFailedAttempt(ctx context.Context, email string) {
	enabled, _, duration := s.lockoutConfig(ctx)
	if !enabled {
		return
	}
	lockoutKey := fmt.Sprintf("lockout:%s", email)
	countStr, _ := s.cacheService.Get(ctx, lockoutKey)
	count := 0
	if countStr != "" {
		_, _ = fmt.Sscanf(countStr, "%d", &count)
	}
	count++
	if duration == 0 {
		duration = 15 * time.Minute
	}
	if err := s.cacheService.Set(ctx, lockoutKey, fmt.Sprintf("%d", count), duration); err != nil {
		s.logger.Error("Failed to record lockout attempt", err, "email", email)
	}
}

func (s *Service) clearFailedAttempts(ctx context.Context, email string) {
	enabled, _, _ := s.lockoutConfig(ctx)
	if !enabled {
		return
	}
	lockoutKey := fmt.Sprintf("lockout:%s", email)
	if err := s.cacheService.Delete(ctx, lockoutKey); err != nil {
		s.logger.Error("Failed to clear lockout", err, "email", email)
	}
}

func (s *Service) CreateAPIKey(ctx context.Context, userID string, req types.CreateAPIKeyRequest) (*types.APIKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("failed to generate api key: %w", err)
	}
	keyStr := s.config.Auth.APIKeyPrefix + hex.EncodeToString(raw)

	apiKey := &types.APIKey{
		ID:        uuid.New().String(),
		UserID:    userID,
		Name:      req.Name,
		Key:       keyStr,
		Prefix:    s.config.Auth.APIKeyPrefix,
		Active:    true,
		CreatedAt: time.Now(),
	}

	if err := s.dbService.CreateAPIKey(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("failed to store api key: %w", err)
	}

	return apiKey, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, userID string) ([]*types.APIKey, error) {
	keys, err := s.dbService.ListAPIKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys: %w", err)
	}
	for _, k := range keys {
		k.Key = ""
	}
	return keys, nil
}

func (s *Service) DeleteAPIKey(ctx context.Context, userID, keyID string) error {
	existing, err := s.dbService.GetAPIKey(ctx, userID, keyID)
	if err != nil {
		return fmt.Errorf("failed to get api key: %w", err)
	}
	if existing == nil {
		return errors.New("api key not found")
	}
	return s.dbService.DeleteAPIKey(ctx, userID, keyID)
}

// extractToken extracts the token from the Authorization header or cookie
func extractToken(c *gin.Context) string {
	return utilities.ExtractToken(c, utilities.TokenExtractorConfig{
		HeaderName: "Authorization",
		TokenType:  "Bearer",
		CookieName: "lsp_session",
	})
}

// AuthMiddleware returns a middleware that validates JWT tokens
func (s *Service) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract token from request
		tokenString := extractToken(c)
		if tokenString == "" {
			c.JSON(401, gin.H{"error": "Authorization token required"})
			c.Abort()
			return
		}

		// Validate token
		userID, err := s.ValidateToken(tokenString)
		if err != nil {
			c.JSON(401, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		// Set user ID in context
		c.Set("userID", userID)

		// Set session ID (JWT jti) for DEK cache lookup in secret management.
		if jti := utilities.ExtractJTI(tokenString); jti != "" {
			c.Set("sessionID", jti)
		}

		// Load user role into context for AdminGuard and authorization checks.
		if s.dbService != nil {
			if user, err := s.dbService.GetUser(c.Request.Context(), userID); err == nil && user != nil {
				c.Set("userRole", user.Role)
			}
		}

		c.Next()
	}
}

// OptionalAuthMiddleware is like AuthMiddleware but never aborts. It sets
// "userID" in the context when a valid JWT/API key is present, and calls
// c.Next() unconditionally. Handlers that use this middleware must check
// the userID themselves and handle the unauthenticated case.
func (s *Service) OptionalAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := extractToken(c)
		if tokenString != "" {
			userID, err := s.ValidateToken(tokenString)
			if err == nil && userID != "" {
				c.Set("userID", userID)
				if jti := utilities.ExtractJTI(tokenString); jti != "" {
					c.Set("sessionID", jti)
				}
				if s.dbService != nil {
					if user, err := s.dbService.GetUser(c.Request.Context(), userID); err == nil && user != nil {
						c.Set("userRole", user.Role)
					}
				}
			}
		}
		c.Next()
	}
}

// The keyFunc closure is shared between ValidateToken and
// RevokeToken so both surfaces honor the rotated-key list.
func (s *Service) parseTokenAcceptingRotatedKeys(token string) (*jwt.Token, error) {
	keyFunc := func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		// jwt.Parse calls keyFunc once per parse attempt; we return
		// a slice of candidate keys via a custom multi-key strategy
		// not natively supported by jwt-go. Instead we parse
		// repeatedly: first with the active key, then with each
		// previous key. The first non-error parse wins.
		return s.jwtSecret, nil
	}
	parsed, err := jwt.Parse(token, keyFunc)
	if err == nil && parsed.Valid {
		return parsed, nil
	}
	// Fall through: try each previous key. We re-parse with a
	// fresh keyFunc that returns the candidate.
	var lastErr error
	for _, prev := range s.jwtPreviousSecrets {
		altKeyFunc := func(prevKey []byte) jwt.Keyfunc {
			return func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return prevKey, nil
			}
		}(prev)
		alt, altErr := jwt.Parse(token, altKeyFunc)
		if altErr == nil && alt.Valid {
			return alt, nil
		}
		lastErr = altErr
	}
	if err != nil {
		return nil, err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("token signature does not verify against any active or previous key")
}
