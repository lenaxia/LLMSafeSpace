// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package mocks

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// MockAuthMiddlewareService is a mock implementation of the AuthService interface for middleware tests
type MockAuthMiddlewareService struct {
	mock.Mock
}

func (m *MockAuthMiddlewareService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAuthMiddlewareService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAuthMiddlewareService) ValidateToken(ctx context.Context, token string) (string, error) {
	args := m.Called(token)
	return args.String(0), args.Error(1)
}

// RevokeToken records the call and returns the configured error. Used by
// /auth/logout (G18, Epic 17). Tests that don't care about revocation
// can omit `On("RevokeToken", ...)` — the mock returns nil by default
// when called via the testify framework.
func (m *MockAuthMiddlewareService) RevokeToken(_ context.Context, token string) error {
	args := m.Called(token)
	return args.Error(0)
}

// MarkUserSuspended records the F4 revocation-marker write for assertion. No-op
// by default (returns nil) so tests that do not exercise the suspend path are
// unaffected.
func (m *MockAuthMiddlewareService) MarkUserSuspended(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

// ClearUserSuspended records the F4 marker-clear on unsuspend.
func (m *MockAuthMiddlewareService) ClearUserSuspended(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockAuthMiddlewareService) GetUserID(c *gin.Context) string {
	args := m.Called(c)
	return args.String(0)
}

func (m *MockAuthMiddlewareService) CheckResourceAccess(userID, resourceType, resourceID, action string) bool {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0)
}

func (m *MockAuthMiddlewareService) GenerateToken(userID string) (string, error) {
	args := m.Called(userID)
	return args.String(0), args.Error(1)
}

func (m *MockAuthMiddlewareService) AuthenticateAPIKey(ctx context.Context, apiKey string) (string, error) {
	args := m.Called(ctx, apiKey)
	return args.String(0), args.Error(1)
}

func (m *MockAuthMiddlewareService) Register(ctx context.Context, req types.RegisterRequest) (*types.AuthResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.AuthResponse), args.Error(1)
}

func (m *MockAuthMiddlewareService) Login(ctx context.Context, req types.LoginRequest) (*types.AuthResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.AuthResponse), args.Error(1)
}

func (m *MockAuthMiddlewareService) CreateAPIKey(ctx context.Context, userID string, req types.CreateAPIKeyRequest, sessionID string, matchedSigningKey []byte) (*types.APIKey, error) {
	args := m.Called(ctx, userID, req, sessionID, matchedSigningKey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.APIKey), args.Error(1)
}

func (m *MockAuthMiddlewareService) ListAPIKeys(ctx context.Context, userID string) ([]*types.APIKey, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*types.APIKey), args.Error(1)
}

func (m *MockAuthMiddlewareService) DeleteAPIKey(ctx context.Context, userID, keyID string) error {
	args := m.Called(ctx, userID, keyID)
	return args.Error(0)
}

func (m *MockAuthMiddlewareService) AuthMiddleware() gin.HandlerFunc {
	args := m.Called()
	return args.Get(0).(gin.HandlerFunc)
}

func (m *MockAuthMiddlewareService) OptionalAuthMiddleware() gin.HandlerFunc {
	args := m.Called()
	return args.Get(0).(gin.HandlerFunc)
}
