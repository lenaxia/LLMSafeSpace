// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/lenaxia/llmsafespaces/api/internal/config"
	apierrors "github.com/lenaxia/llmsafespaces/api/internal/errors"
	"github.com/lenaxia/llmsafespaces/api/internal/interfaces"
	"github.com/lenaxia/llmsafespaces/api/internal/logger"
	"github.com/lenaxia/llmsafespaces/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespaces/api/internal/utilities"
	"github.com/lenaxia/llmsafespaces/pkg/secrets"
	"github.com/lenaxia/llmsafespaces/pkg/settings"
	"github.com/lenaxia/llmsafespaces/pkg/types"
	pkgutil "github.com/lenaxia/llmsafespaces/pkg/utilities"
)

// KeyServiceInterface abstracts the key service for DEK lifecycle.
type KeyServiceInterface interface {
	InitializeUserKeys(ctx context.Context, userID string, password []byte) (recoveryKeyHex string, err error)
	UnlockDEK(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration) error
	HasKeys(ctx context.Context, userID string) (bool, error)
	GetDEK(ctx context.Context, sessionID string) ([]byte, error)
	CacheDEK(ctx context.Context, sessionID string, dek []byte, ttl time.Duration) error
}

// SetKeyService sets the optional key service for secret management.
func (s *Service) SetKeyService(ks KeyServiceInterface) {
	s.keyService = ks
}

// EmailVerifier creates and sends email-verification tokens for new users.
// When set, Register creates an unverified account (email_verified=false)
// and calls Verify to send the verification link. When nil, Register marks
// the account email_verified=true immediately (dev/air-gapped mode — no
// email provider to verify with).
type EmailVerifier interface {
	SendVerification(ctx context.Context, userID, email string) error
}

// ErrEmailNotVerified is returned by Login when the credentials are correct
// but the user has not verified their email address. The caller (handler)
// maps this to 403 with a clear message directing the user to check their
// email. Not recorded as a failed login attempt (the credentials are valid).
var ErrEmailNotVerified = errors.New("please verify your email address before logging in")

// SetEmailVerifier wires the email-verification hook. Optional — nil means
// Register auto-verifies (dev mode without an email provider).
func (s *Service) SetEmailVerifier(v EmailVerifier) {
	s.emailVerifier = v
}

// SetInstanceSettings injects the instance settings service for runtime config reads.
func (s *Service) SetInstanceSettings(svc interfaces.SettingsReader) {
	s.instanceSettings = svc
}

// SetMasterKey sets the server master key used for encrypting API key ciphertext
// (enabling DEK re-wrap on rotation). Derived from LLMSAFESPACES_MASTER_SECRET.
func (s *Service) SetMasterKey(key []byte) {
	provider, err := secrets.NewStaticKeyProvider(key)
	if err != nil {
		return
	}
	s.rootKeyProvider = provider
}

// SetRootKeyProvider sets the RootKeyProvider for API key at-rest encryption.
func (s *Service) SetRootKeyProvider(provider secrets.RootKeyProvider) {
	s.rootKeyProvider = provider
}

const defaultAPIKeyDEKTTL = 24 * time.Hour

func (s *Service) apiKeyDEKTTL() time.Duration {
	if s.config.Auth.APIKeyDEKTTL > 0 {
		return s.config.Auth.APIKeyDEKTTL
	}
	return defaultAPIKeyDEKTTL
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
	if v, err := s.instanceSettings.GetBool(ctx, settings.KeyAuthLockoutEnabled.Name()); err == nil {
		enabled = v
	}
	if v, err := s.instanceSettings.GetInt(ctx, settings.KeyAuthLockoutAttempts.Name()); err == nil && v > 0 {
		attempts = v
	}
	if v, err := s.instanceSettings.GetInt(ctx, settings.KeyAuthLockoutDurationMinutes.Name()); err == nil && v > 0 {
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
	// maxTokenTTL is the maximum lifetime of any token the service issues
	// (max(tokenDuration, rememberMeDuration)). Used as the TTL for the F4
	// user-suspension marker so it outlives EVERY outstanding token — including
	// remember-me tokens (720h default), which outlast standard tokens (24h).
	maxTokenTTL      time.Duration
	keyService       KeyServiceInterface
	emailVerifier    EmailVerifier
	instanceSettings interfaces.SettingsReader
	rootKeyProvider  secrets.RootKeyProvider
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
	cacheKey := fmt.Sprintf("apikey:%s", pkgutil.HashString(apiKey))

	// Try to get from cache first
	cachedStatus, err := s.cacheService.Get(ctx, cacheKey)
	if err == nil && cachedStatus != "" {
		if cachedStatus == "revoked" {
			return "", errors.New("token has been revoked")
		}
		return cachedStatus, nil
	}

	// Hash-first lookup (new keys). Fall back to plaintext for legacy keys. (Epic 10 US-10.13)
	h := sha256.Sum256([]byte(apiKey))
	keyHash := hex.EncodeToString(h[:])
	user, err := s.dbService.GetUserByAPIKey(ctx, keyHash)
	if err != nil {
		return "", fmt.Errorf("failed to authenticate API key: %w", err)
	}
	if user == nil {
		// Legacy plaintext fallback — only for pre-000017 keys (short tokens).
		// Real API tokens are 64-char hex hashes, not plaintext.
		if len(apiKey) != 64 {
			user, err = s.dbService.GetUserByAPIKey(ctx, apiKey)
			if err != nil {
				return "", fmt.Errorf("failed to authenticate API key: %w", err)
			}
			if user != nil {
				s.logger.Warn("Authenticated via legacy plaintext API key — user should rotate", "user_id", user.ID)
			}
		}
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

	// Warn when rememberMeDuration is set but shorter than tokenDuration —
	// this means remember-me sessions would expire sooner than standard sessions,
	// almost certainly a misconfiguration. We allow it (could be intentional
	// during incident response) but make it visible at startup.
	if cfg.Auth.RememberMeDuration > 0 && cfg.Auth.RememberMeDuration < cfg.Auth.TokenDuration {
		log.Warn("auth: rememberMeDuration is shorter than tokenDuration; "+
			"remember-me sessions will expire sooner than standard sessions — check your configuration",
			"rememberMeDuration", cfg.Auth.RememberMeDuration,
			"tokenDuration", cfg.Auth.TokenDuration)
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
		// F4: the suspension marker must outlive the longest-lived token so it
		// stays enforceable for remember-me sessions too. rememberMeDuration
		// defaults to 720h vs tokenDuration's 24h; take the max so the marker
		// never expires before a still-valid token would.
		maxTokenTTL: suspensionMarkerTTL(cfg.Auth.TokenDuration, cfg.Auth.RememberMeDuration),
	}, nil
}

// suspensionMarkerTTL returns the TTL the F4 revocation marker must use so it
// covers every outstanding token. It is max(tokenDuration, rememberMeDuration)
// so remember-me sessions (which outlast standard tokens) stay gated until the
// marker is explicitly cleared by UnsuspendUser or natural token expiry.
func suspensionMarkerTTL(tokenDuration, rememberMeDuration time.Duration) time.Duration {
	if rememberMeDuration > tokenDuration {
		return rememberMeDuration
	}
	return tokenDuration
}

// GetUserID gets the user ID from the context
func (s *Service) GetUserID(c *gin.Context) string {
	userID, exists := c.Get("userID")
	if !exists {
		return ""
	}
	return userID.(string)
}

// userSuspendedKey is the Redis key holding the per-user revocation marker
// written by MarkUserSuspended. A live value means "deny this user's currently
// issued tokens immediately, without a DB lookup" (F4, US-43.19).
func userSuspendedKey(userID string) string { return "user_suspended:" + userID }

// MarkUserSuspended writes a per-user revocation marker so the auth middleware
// rejects the user's existing JWTs/API keys the instant the admin suspends them,
// without waiting for the next per-request GetUser or depending on the DB (which
// may be briefly unavailable). The TTL is max(tokenDuration, rememberMeDuration)
// so the marker outlives every outstanding token — including remember-me
// sessions (720h default), which outlast standard tokens (24h). Unsuspends call
// ClearUserSuspended for an immediate recovery (no TTL wait).
func (s *Service) MarkUserSuspended(ctx context.Context, userID string) error {
	if err := s.cacheService.Set(ctx, userSuspendedKey(userID), "1", s.maxTokenTTL); err != nil {
		return fmt.Errorf("failed to mark user suspended: %w", err)
	}
	return nil
}

// ClearUserSuspended removes the revocation marker so an unsuspended user's
// existing tokens work again immediately (no TTL wait).
func (s *Service) ClearUserSuspended(ctx context.Context, userID string) error {
	if err := s.cacheService.Delete(ctx, userSuspendedKey(userID)); err != nil {
		return fmt.Errorf("failed to clear user suspended marker: %w", err)
	}
	return nil
}

// isUserSuspendedCached reports whether a live revocation marker exists for the
// user. A miss (or Redis error) returns false — the authoritative GetUser check
// in the middleware still runs and fail-closes on DB error. This is purely a
// fast-path + DB-outage-resilience layer, never the sole enforcement.
func (s *Service) isUserSuspendedCached(ctx context.Context, userID string) bool {
	v, err := s.cacheService.Get(ctx, userSuspendedKey(userID))
	return err == nil && v != ""
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
	hashKey := "token:" + pkgutil.HashString(token)
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

// GenerateToken generates a JWT token for a user using the configured tokenDuration.
// It delegates to GenerateTokenWithDuration, which is the canonical implementation.
func (s *Service) GenerateToken(userID string) (string, error) {
	return s.GenerateTokenWithDuration(userID, s.tokenDuration)
}

// GenerateTokenWithDuration generates a JWT token for a user with an explicit TTL.
// This is the canonical token-generation implementation; GenerateToken delegates here.
// Not exposed on the AuthService interface — callers outside the auth package use
// GenerateToken, which always uses the configured tokenDuration.
func (s *Service) GenerateTokenWithDuration(userID string, duration time.Duration) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"jti": uuid.New().String(),
		"exp": time.Now().Add(duration).Unix(),
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
	return s.ValidateTokenWithClientIP(tokenString, "")
}

// ValidateTokenWithClientIP validates a JWT token or API key, enforcing
// allowed_cidrs when clientIP is non-empty.
func (s *Service) ValidateTokenWithClientIP(tokenString, clientIP string) (string, error) {
	if utilities.IsAPIKey(tokenString, s.config.Auth.APIKeyPrefix) {
		return s.validateAPIKey(tokenString, clientIP)
	}

	// Check if token is cached
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cacheKey := fmt.Sprintf("token:%s", pkgutil.HashString(tokenString))

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

// validateAPIKey validates an API key (internal method).
// clientIP is optional; when provided, allowed_cidrs is enforced.
func (s *Service) validateAPIKey(apiKey, clientIP string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cacheKey := fmt.Sprintf("apikey:%s", pkgutil.HashString(apiKey))

	if cachedUserID, err := s.cacheService.Get(ctx, cacheKey); err == nil && cachedUserID != "" {
		if cachedUserID == "revoked" {
			return "", errors.New("token has been revoked")
		}
		if clientIP != "" && s.rootKeyProvider != nil && utilities.IsAPIKey(apiKey, s.config.Auth.APIKeyPrefix) {
			h := sha256.Sum256([]byte(apiKey))
			keyHash := hex.EncodeToString(h[:])
			keyRec, dbErr := s.dbService.GetAPIKeyRecordByHash(ctx, keyHash)
			if dbErr == nil && keyRec != nil && len(keyRec.AllowedCIDRs) > 0 {
				if !ipInAnyCIDR(clientIP, keyRec.AllowedCIDRs) {
					return "", errors.New("request source IP not in allowed ranges for this key")
				}
			}
		}
		return cachedUserID, nil
	}

	h := sha256.Sum256([]byte(apiKey))
	keyHash := hex.EncodeToString(h[:])
	user, err := s.dbService.GetUserByAPIKey(ctx, keyHash)
	if err != nil {
		return "", fmt.Errorf("failed to get user by API key: %w", err)
	}
	if user == nil && len(apiKey) != 64 {
		user, err = s.dbService.GetUserByAPIKey(ctx, apiKey)
		if err != nil {
			return "", fmt.Errorf("failed to get user by API key: %w", err)
		}
	}

	if user == nil {
		return "", errors.New("invalid API key")
	}

	if s.rootKeyProvider != nil && utilities.IsAPIKey(apiKey, s.config.Auth.APIKeyPrefix) {
		keyRec, dbErr := s.dbService.GetAPIKeyRecordByHash(ctx, keyHash)
		if dbErr != nil {
			s.logger.Error("Failed to get API key record", dbErr, "key_hash", keyHash)
		} else if keyRec != nil {
			if len(keyRec.AllowedCIDRs) > 0 && clientIP != "" {
				if !ipInAnyCIDR(clientIP, keyRec.AllowedCIDRs) {
					return "", errors.New("request source IP not in allowed ranges for this key")
				}
			}

			if len(keyRec.KeyCiphertext) > 0 {
				storedRaw, decErr := s.rootKeyProvider.Decrypt(ctx, keyRec.KeyCiphertext)
				if decErr != nil {
					s.logger.Error("Failed to decrypt key_ciphertext", decErr, "key_id", keyRec.ID)
				} else {
					if subtle.ConstantTimeCompare(storedRaw, []byte(apiKey)) != 1 {
						zeroBytes(storedRaw)
						return "", errors.New("invalid API key")
					}
					zeroBytes(storedRaw)
				}
			}

			if keyRec.DecryptAccess && len(keyRec.WrappedDEK) > 0 && len(keyRec.KekSalt) > 0 {
				if !keyRec.DekSynced {
					s.logger.Warn("API key DEK re-sync in progress", "key_id", keyRec.ID)
				} else {
					apiKEK, deriveErr := secrets.DeriveKEKFromKey([]byte(apiKey), keyRec.KekSalt, "llmsafespaces-apikey-kek")
					if deriveErr != nil {
						s.logger.Error("Failed to derive API KEK", deriveErr)
					} else {
						dek, decErr := secrets.DecryptSecret(apiKEK, keyRec.WrappedDEK)
						if decErr != nil {
							s.logger.Error("Failed to unwrap DEK for API key", decErr, "key_id", keyRec.ID)
						} else {
							sessionID := "apikey:" + pkgutil.HashString(apiKey)
							if cacheErr := s.keyService.CacheDEK(ctx, sessionID, dek, s.apiKeyDEKTTL()); cacheErr != nil {
								s.logger.Error("Failed to cache DEK for API key session", cacheErr, "session_id", sessionID)
							}
						}
					}
				}
			}
		}
	} else if s.keyService != nil && utilities.IsAPIKey(apiKey, s.config.Auth.APIKeyPrefix) {
		keyRec, dbErr := s.dbService.GetAPIKeyRecordByHash(ctx, keyHash)
		if dbErr != nil {
			s.logger.Error("Failed to get API key record for DEK check", dbErr, "key_hash", keyHash)
		} else if keyRec != nil && keyRec.DecryptAccess && len(keyRec.WrappedDEK) > 0 && len(keyRec.KekSalt) > 0 {
			apiKEK, deriveErr := secrets.DeriveKEKFromKey([]byte(apiKey), keyRec.KekSalt, "llmsafespaces-apikey-kek")
			if deriveErr != nil {
				s.logger.Error("Failed to derive API KEK", deriveErr)
			} else {
				dek, decErr := secrets.DecryptSecret(apiKEK, keyRec.WrappedDEK)
				if decErr != nil {
					s.logger.Error("Failed to unwrap DEK for API key", decErr, "key_id", keyRec.ID)
				} else {
					sessionID := "apikey:" + pkgutil.HashString(apiKey)
					if cacheErr := s.keyService.CacheDEK(ctx, sessionID, dek, s.apiKeyDEKTTL()); cacheErr != nil {
						s.logger.Error("Failed to cache DEK for API key session", cacheErr, "session_id", sessionID)
					}
				}
			}
		}
	}

	err = s.cacheService.Set(ctx, cacheKey, user.ID, 15*time.Minute)
	if err != nil {
		s.logger.Error("Failed to cache API key", err, "user_id", user.ID)
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

	// US-49.6: Send email verification. When an email verifier is wired
	// (SES in production), the user starts unverified and must click the
	// link before they can log in. When no verifier is wired (dev/air-gapped),
	// persist email_verified=true immediately so Login (which reads from DB)
	// doesn't permanently lock the user out.
	if s.emailVerifier != nil {
		if err := s.emailVerifier.SendVerification(ctx, userID, user.Email); err != nil {
			s.logger.Warn("Register: failed to send verification email", "user_id", userID, "error", err.Error())
		}
		user.EmailVerified = false
	} else {
		verified := true
		_ = s.dbService.UpdateUser(ctx, userID, types.UserUpdates{EmailVerified: &verified})
		user.EmailVerified = true
	}

	user.PasswordHash = ""
	return &types.AuthResponse{Token: token, User: *user, RecoveryKey: recoveryKey, TokenTTL: s.tokenDuration}, nil
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
		metrics.RecordAuthFailure("user_not_found")
		// G27: same as VerifyPassword — burn the bcrypt cycles so
		// no-such-user takes ~226ms instead of ~16ms.
		_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(req.Password))
		return nil, errors.New("invalid email or password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		s.recordFailedAttempt(ctx, email)
		metrics.RecordAuthFailure("wrong_password")
		return nil, errors.New("invalid email or password")
	}

	if user.Status == types.UserStatusSuspended {
		s.recordFailedAttempt(ctx, email)
		metrics.RecordAuthFailure("account_suspended")
		return nil, errors.New("account suspended")
	}

	if !user.Active {
		s.recordFailedAttempt(ctx, email)
		metrics.RecordAuthFailure("account_inactive")
		return nil, errors.New("invalid email or password")
	}

	// US-49.6: Unverified users cannot log in. The credentials are correct
	// (we checked bcrypt above), so it's safe to tell them WHY — they need
	// to verify their email. This does not create an enumeration vector:
	// the attacker already knows the email AND the password to reach this
	// branch.
	if !user.EmailVerified {
		metrics.RecordAuthFailure("email_not_verified")
		return nil, ErrEmailNotVerified
	}

	s.clearFailedAttempts(ctx, email)

	// Determine effective token TTL: use rememberMeDuration when the user
	// opts in and the feature is configured, otherwise use tokenDuration.
	tokenDur := s.tokenDuration
	if req.RememberMe && s.config.Auth.RememberMeDuration > 0 {
		tokenDur = s.config.Auth.RememberMeDuration
	}

	token, err := s.GenerateTokenWithDuration(user.ID, tokenDur)
	if err != nil {
		return nil, errors.New("login failed")
	}

	// Extract jti once — used for both DEK unlock and session tracking.
	jti := utilities.ExtractJTI(token)

	// Unlock DEK for secret management (Epic 10)
	if s.keyService != nil {
		if jti != "" {
			// Auto-initialize keys for pre-Epic 10 users on first login
			hasKeys, _ := s.keyService.HasKeys(ctx, user.ID)
			if !hasKeys {
				if _, err := s.keyService.InitializeUserKeys(ctx, user.ID, []byte(req.Password)); err != nil {
					s.logger.Warn("Login: failed to auto-init keys", "user_id", user.ID, "error", err.Error())
				}
			}
			if err := s.keyService.UnlockDEK(ctx, user.ID, []byte(req.Password), jti, tokenDur); err != nil {
				s.logger.Warn("Login: failed to unlock DEK", "user_id", user.ID, "error", err.Error())
			}
		}
	}

	// US-49.5: Track the jti for bulk session invalidation on password
	// reset. Best-effort: if Redis is unavailable, login still succeeds;
	// the session just won't be revocable in bulk (the token TTL bounds
	// the exposure).
	if jti != "" {
		s.trackUserSession(ctx, user.ID, jti, token, tokenDur)
	}

	user.PasswordHash = ""
	return &types.AuthResponse{Token: token, User: *user, TokenTTL: tokenDur}, nil
}

// trackUserSession records the session's Redis keys for bulk revocation.
// Stores both the jti key and the hash key so RevokeAllUserSessions can
// write "revoked" under both — matching RevokeToken's approach (the hash-key
// fast-path in ValidateToken must also see the revocation). Best-effort:
// errors AND panics are swallowed (test mocks for cacheService panic on
// unexpected calls; login must never fail because session tracking is
// unavailable). The set is capped at 50 entries.
func (s *Service) trackUserSession(ctx context.Context, userID, jti, token string, ttl time.Duration) {
	if jti == "" {
		return
	}
	defer func() {
		_ = recover() // best-effort: tracking must never break login
	}()
	key := "user-sessions:" + userID
	hashKey := "token:" + pkgutil.HashString(token)
	entry := jti + "|" + hashKey
	var entries []string
	_ = s.cacheService.GetObject(ctx, key, &entries)
	entries = append(entries, entry)
	if len(entries) > 50 {
		entries = entries[len(entries)-50:]
	}
	storeTTL := s.maxSessionRevocationTTL()
	if err := s.cacheService.SetObject(ctx, key, entries, storeTTL); err != nil && s.logger != nil {
		s.logger.Warn("Login: failed to track session for revocation", "user_id", userID)
	}
}

// maxSessionRevocationTTL returns the longest possible token TTL so the
// revocation entry outlives the token. Uses RememberMeDuration if configured
// (up to 30d), otherwise TokenDuration.
func (s *Service) maxSessionRevocationTTL() time.Duration {
	ttl := s.tokenDuration
	if s.config.Auth.RememberMeDuration > ttl {
		ttl = s.config.Auth.RememberMeDuration
	}
	return ttl
}

// RevokeAllUserSessions revokes all outstanding JWTs for a user by writing
// "revoked" under each tracked jti key AND hash key (both paths that
// ValidateToken checks). Used by password-reset confirm (US-49.5) so a
// stolen JWT stops working after the victim resets their password.
func (s *Service) RevokeAllUserSessions(userID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "user-sessions:" + userID
	var entries []string
	if err := s.cacheService.GetObject(ctx, key, &entries); err != nil || len(entries) == 0 {
		return nil
	}

	revokedTTL := s.maxSessionRevocationTTL()
	for _, entry := range entries {
		parts := strings.SplitN(entry, "|", 2)
		jti := parts[0]
		// Write jti key — catches the jti-based revocation check in ValidateToken.
		_ = s.cacheService.Set(ctx, "token:"+jti, "revoked", revokedTTL)
		// Write hash key — catches the hash-key fast-path in ValidateToken
		// (which returns the cached value as userID; "revoked" is not a valid
		// userID so the middleware rejects the request).
		if len(parts) > 1 {
			_ = s.cacheService.Set(ctx, parts[1], "revoked", revokedTTL)
		}
	}
	_ = s.cacheService.Delete(ctx, key)
	return nil
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

func (s *Service) CreateAPIKey(ctx context.Context, userID string, req types.CreateAPIKeyRequest, sessionID string) (*types.APIKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("failed to generate api key: %w", err)
	}
	keyStr := s.config.Auth.APIKeyPrefix + hex.EncodeToString(raw)

	h := sha256.Sum256([]byte(keyStr))
	keyHash := hex.EncodeToString(h[:])
	keyPrefix := keyStr
	if len(keyPrefix) > 8 {
		keyPrefix = keyPrefix[:8]
	}

	apiKey := &types.APIKey{
		ID:           uuid.New().String(),
		UserID:       userID,
		Name:         req.Name,
		Key:          keyHash,
		Prefix:       keyPrefix,
		Active:       true,
		CreatedAt:    time.Now(),
		Legacy:       false,
		AllowedCIDRs: req.AllowedCIDRs,
	}

	if req.DecryptAccess {
		if s.rootKeyProvider == nil {
			return nil, errors.New("server root key not configured; decrypt_access keys unavailable")
		}
		if sessionID == "" {
			return nil, errors.New("JWT session required to create a key with decrypt_access=true")
		}
		if s.keyService == nil {
			return nil, errors.New("key service not configured; decrypt_access keys unavailable")
		}

		dek, err := s.keyService.GetDEK(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("DEK not available for wrapping: %w", err)
		}

		kekSalt := make([]byte, 32)
		if _, err := rand.Read(kekSalt); err != nil {
			return nil, fmt.Errorf("failed to generate KEK salt: %w", err)
		}

		apiKEK, err := secrets.DeriveKEKFromKey([]byte(keyStr), kekSalt, "llmsafespaces-apikey-kek")
		if err != nil {
			return nil, fmt.Errorf("failed to derive API KEK: %w", err)
		}

		wrappedDEK, err := secrets.EncryptSecret(apiKEK, dek)
		if err != nil {
			return nil, fmt.Errorf("failed to wrap DEK: %w", err)
		}

		apiKey.DecryptAccess = true
		apiKey.KekSalt = kekSalt
		apiKey.WrappedDEK = wrappedDEK
		apiKey.DekSynced = true
	}

	if s.rootKeyProvider != nil {
		keyCiphertext, err := s.rootKeyProvider.Encrypt(ctx, []byte(keyStr))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt key ciphertext: %w", err)
		}
		apiKey.KeyCiphertext = keyCiphertext
	}

	if err := s.dbService.CreateAPIKey(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("failed to store api key: %w", err)
	}

	apiKey.Key = keyStr
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

// extractToken extracts the JWT or API-key token from the Authorization header
// or the configured session cookie. The cookie name is read from the service
// config (cfg.Auth.CookieName) with a fallback to "lsp_session".
func (s *Service) extractToken(c *gin.Context) string {
	name := s.config.Auth.CookieName
	if name == "" {
		name = "lsp_session"
	}
	return utilities.ExtractToken(c, utilities.TokenExtractorConfig{
		HeaderName: "Authorization",
		TokenType:  "Bearer",
		CookieName: name,
	})
}

// AuthMiddleware returns a middleware that validates JWT tokens
func (s *Service) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract token from request
		tokenString := s.extractToken(c)
		if tokenString == "" {
			c.JSON(401, gin.H{"error": "Authorization token required"})
			c.Abort()
			return
		}

		// Validate token
		userID, err := s.ValidateTokenWithClientIP(tokenString, c.ClientIP())
		if err != nil {
			c.JSON(401, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		// Set user ID in context
		c.Set("userID", userID)

		// Set session ID for DEK cache lookup in secret management.
		if jti := utilities.ExtractJTI(tokenString); jti != "" {
			c.Set("sessionID", jti)
		} else if utilities.IsAPIKey(tokenString, s.config.Auth.APIKeyPrefix) {
			c.Set("sessionID", "apikey:"+pkgutil.HashString(tokenString))
		}

		// Load user role into context for AdminGuard and authorization checks.
		// D19: also enforce user-level suspension here — this is the single
		// load-bearing gate that blocks a suspended user from EVERY
		// authenticated endpoint (all orgs + personal). A suspended user's
		// token/API key is still cryptographically valid; the status check is
		// what denies access.
		//
		// F3 (US-43.19): FAIL CLOSED on any GetUser error — the previous code
		// silently fell through to c.Next(), letting a suspended user regain
		// access during a DB blip. Denying legitimate users during a DB outage
		// is the correct security posture for an authz gate.
		//
		// F4 (US-43.19): the revocation marker set by SuspendUser lets us
		// report a precise 401 (not 503) for a suspended user EVEN when the DB
		// is unreachable, and lets us HEAL a stale marker left by an unsuspend
		// whose ClearUserSuspended failed (Redis blip) — otherwise an active
		// user would be falsely blocked until the marker TTL expired. GetUser
		// remains authoritative; the marker is only consulted on the DB-error
		// branch (resilience) and the active-user branch (healing).
		if s.dbService != nil {
			user, gerr := s.dbService.GetUser(c.Request.Context(), userID)
			if gerr != nil {
				if s.isUserSuspendedCached(c.Request.Context(), userID) {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "account suspended"})
					return
				}
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "unable to verify account status"})
				return
			}
			if user == nil {
				// Token validated but no user row — the account was deleted
				// while the token was still cryptographically valid. Fail
				// closed rather than honoring a stale credential.
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "account not found"})
				return
			}
			if user.Status == types.UserStatusSuspended {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "account suspended"})
				return
			}
			// Active user. Clear any stale revocation marker (an unsuspend
			// whose ClearUserSuspended failed) so the next request is not
			// falsely flagged. Best-effort: a Redis failure here only leaves
			// the marker to expire on its own TTL.
			if s.isUserSuspendedCached(c.Request.Context(), userID) {
				_ = s.ClearUserSuspended(c.Request.Context(), userID)
			}
			c.Set("userRole", user.Role)
		}

		c.Next()
	}
}

// OptionalAuthMiddleware is like AuthMiddleware but never aborts. It sets
// "userID" in the context when a valid JWT/API key is present, and calls
// c.Next() unconditionally. Handlers that use this middleware must check
// the userID themselves and handle the unauthenticated case.
//
// D19: a suspended user is treated as unauthenticated here — no userID,
// sessionID, or role is set — so they cannot exercise any authenticated
// capability. They retain access only to the anonymous surface (the same
// surface any unauthenticated caller sees). The middleware still does not
// abort, preserving its contract for public+optional-auth endpoints.
func (s *Service) OptionalAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := s.extractToken(c)
		if tokenString != "" {
			userID, err := s.ValidateTokenWithClientIP(tokenString, c.ClientIP())
			if err == nil && userID != "" {
				suspended := false
				// OptionalAuthMiddleware never aborts; it excludes a suspended
				// user by withholding userID (they get the anonymous surface).
				// On GetUser error it stays anonymous (optional endpoints must
				// keep working for unauthenticated callers during a DB blip);
				// the mandatory AuthMiddleware fail-closes, this one does not.
				// The F4 marker is intentionally NOT consulted here: it would
				// add a stale-marker false-positive risk for no benefit, since
				// GetUser already authoritatively resolves suspension.
				if s.dbService != nil {
					if user, gerr := s.dbService.GetUser(c.Request.Context(), userID); gerr == nil && user != nil {
						if user.Status == types.UserStatusSuspended {
							suspended = true
						} else {
							c.Set("userRole", user.Role)
						}
					}
				}
				if !suspended {
					c.Set("userID", userID)
					if jti := utilities.ExtractJTI(tokenString); jti != "" {
						c.Set("sessionID", jti)
					} else if utilities.IsAPIKey(tokenString, s.config.Auth.APIKeyPrefix) {
						c.Set("sessionID", "apikey:"+pkgutil.HashString(tokenString))
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

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

func ipInAnyCIDR(clientIP string, cidrs []string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
