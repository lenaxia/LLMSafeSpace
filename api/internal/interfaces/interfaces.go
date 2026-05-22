package interfaces

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	k8sinterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type SessionManager interface {
	CreateSession(userID, sandboxID string, conn types.WSConnection) (*types.Session, error)
	GetSession(sessionID string) (*types.Session, error)
	CloseSession(sessionID string)
	SetCancellationFunc(sessionID, executionID string, cancel context.CancelFunc)
	CancelExecution(sessionID, executionID string) bool
	Start() error
	Stop() error
}

type AuthService interface {
	GetUserID(c *gin.Context) string
	CheckResourceAccess(userID, resourceType, resourceID, action string) bool
	GenerateToken(userID string) (string, error)
	ValidateToken(token string) (string, error)
	AuthenticateAPIKey(ctx context.Context, apiKey string) (string, error)
	AuthMiddleware() gin.HandlerFunc
	Start() error
	Stop() error
}

type DatabaseService interface {
	GetUser(ctx context.Context, userID string) (*types.User, error)
	CreateUser(ctx context.Context, user *types.User) error
	UpdateUser(ctx context.Context, userID string, updates map[string]interface{}) error
	DeleteUser(ctx context.Context, userID string) error
	GetUserByAPIKey(ctx context.Context, apiKey string) (*types.User, error)
	GetSandbox(ctx context.Context, sandboxID string) (*types.SandboxMetadata, error)
	CreateSandbox(ctx context.Context, sandbox *types.SandboxMetadata) error
	UpdateSandbox(ctx context.Context, sandboxID string, updates map[string]interface{}) error
	DeleteSandbox(ctx context.Context, sandboxID string) error
	ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]*types.SandboxMetadata, *types.PaginationMetadata, error)
	CheckPermission(userID, resourceType, resourceID, action string) (bool, error)
	CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error)
	Start() error
	Stop() error
}

type CacheService interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, expiration time.Duration) error
	Delete(ctx context.Context, key string) error
	GetObject(ctx context.Context, key string, value interface{}) error
	SetObject(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	GetSession(ctx context.Context, sessionID string) (map[string]interface{}, error)
	SetSession(ctx context.Context, sessionID string, session map[string]interface{}, expiration time.Duration) error
	DeleteSession(ctx context.Context, sessionID string) error
	Start() error
	Stop() error
}

type RateLimiterService interface {
	Increment(ctx context.Context, key string, value int64, expiration time.Duration) (int64, error)
	AddToWindow(ctx context.Context, key string, timestamp int64, member string, expiration time.Duration) error
	RemoveFromWindow(ctx context.Context, key string, cutoff int64) error
	CountInWindow(ctx context.Context, key string, min, max int64) (int, error)
	GetWindowEntries(ctx context.Context, key string, start, stop int) ([]string, error)
	GetTTL(ctx context.Context, key string) (time.Duration, error)
	Allow(key string, rate float64, burst int) bool
	Start() error
	Stop() error
}

type MetricsService interface {
	RecordRequest(method, path string, status int, duration time.Duration, size int)
	RecordSandboxCreation(runtime string, userID string)
	RecordSandboxTermination(runtime, reason string)
	RecordError(errorType, endpoint, code string)
	IncrementActiveConnections(connType string, userID string)
	DecrementActiveConnections(connType string, userID string)
	RecordResourceUsage(sandboxID string, cpu float64, memoryBytes int64)
	Start() error
	Stop() error
}

type SandboxService interface {
	CreateSandbox(ctx context.Context, req *types.CreateSandboxRequest) (*types.Sandbox, error)
	GetSandbox(ctx context.Context, sandboxID string) (*types.Sandbox, error)
	ListSandboxes(ctx context.Context, userID string, limit, offset int) (*types.SandboxListResult, error)
	TerminateSandbox(ctx context.Context, sandboxID string) error
	GetSandboxStatus(ctx context.Context, sandboxID string) (*types.SandboxStatus, error)
	Start() error
	Stop() error
}

type SandboxHandler interface {
	RegisterRoutes(router *gin.RouterGroup)
	HandleWebSocket(c *gin.Context)
}

type Services interface {
	GetAuth() AuthService
	GetDatabase() DatabaseService
	GetCache() CacheService
	GetMetrics() MetricsService
	GetSandbox() SandboxService
}

type KubernetesClient = k8sinterfaces.KubernetesClient
type LLMSafespaceV1Interface = k8sinterfaces.LLMSafespaceV1Interface
type SandboxInterface = k8sinterfaces.SandboxInterface
type RuntimeEnvironmentInterface = k8sinterfaces.RuntimeEnvironmentInterface
type SandboxProfileInterface = k8sinterfaces.SandboxProfileInterface
