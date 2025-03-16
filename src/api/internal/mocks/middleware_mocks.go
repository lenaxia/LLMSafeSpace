package mocks

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
)

// Note: MockMetricsService is already defined in metrics.go
// This file contains additional mock implementations for middleware tests

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

func (m *MockAuthMiddlewareService) ValidateToken(token string) (string, error) {
	args := m.Called(token)
	return args.String(0), args.Error(1)
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

func (m *MockAuthMiddlewareService) AuthMiddleware() gin.HandlerFunc {
	args := m.Called()
	return args.Get(0).(gin.HandlerFunc)
}

// Note: MockRateLimiterService is already defined in ratelimiter.go
