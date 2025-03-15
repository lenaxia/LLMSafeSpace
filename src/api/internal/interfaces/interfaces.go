package interfaces

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	//metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sinterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	//"k8s.io/apimachinery/pkg/watch"
	//"k8s.io/client-go/kubernetes"
	//"k8s.io/client-go/rest"
	
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// SessionManager defines the interface for managing WebSocket sessions
type SessionManager interface {
	CreateSession(userID, sandboxID string, conn types.WSConnection) (*types.Session, error)
	GetSession(sessionID string) (*types.Session, error)
	CloseSession(sessionID string)
	SetCancellationFunc(sessionID, executionID string, cancel context.CancelFunc)
	CancelExecution(sessionID, executionID string) bool
	Start() error
	Stop() error
}

// AuthService defines the interface for authentication services
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

// DatabaseService defines the interface for database services
type DatabaseService interface {
	GetUserByID(ctx context.Context, userID string) (map[string]interface{}, error)
	GetSandboxByID(ctx context.Context, sandboxID string) (map[string]interface{}, error)
	ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error)
	CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error)
	CheckPermission(userID, resourceType, resourceID, action string) (bool, error)
	GetUserIDByAPIKey(ctx context.Context, apiKey string) (string, error)
	CreateSandboxMetadata(ctx context.Context, sandboxID, userID, runtime string) error
	GetSandboxMetadata(ctx context.Context, sandboxID string) (map[string]interface{}, error)
	Start() error
	Stop() error
}

// CacheService defines the interface for cache services
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

// ExecutionService defines the interface for execution services
type ExecutionService interface {
	Execute(ctx context.Context, sandbox *types.Sandbox, execType, content string, timeout int) (*types.ExecutionResult, error)
	ExecuteStream(ctx context.Context, sandbox *types.Sandbox, execType, content string, timeout int, outputCallback func(stream, content string)) (*types.ExecutionResult, error)
	InstallPackages(ctx context.Context, sandbox *types.Sandbox, packages []string, manager string) (*types.ExecutionResult, error)
	Start() error
	Stop() error
}

// FileService defines the interface for file services
type FileService interface {
	ListFiles(ctx context.Context, sandbox *types.Sandbox, path string) ([]types.FileInfo, error)
	DownloadFile(ctx context.Context, sandbox *types.Sandbox, path string) ([]byte, error)
	UploadFile(ctx context.Context, sandbox *types.Sandbox, path string, content []byte) (*types.FileInfo, error)
	DeleteFile(ctx context.Context, sandbox *types.Sandbox, path string) error
	CreateDirectory(ctx context.Context, sandbox *types.Sandbox, path string) (*types.FileInfo, error)
	Start() error
	Stop() error
}

// MetricsService defines the interface for metrics services
type MetricsService interface {
	RecordRequest(method, path string, status int, duration time.Duration, size int)
	RecordSandboxCreation(runtime string, warmPodUsed bool)
	RecordSandboxTermination(runtime string)
	RecordExecution(execType, runtime, status string, duration time.Duration)
	IncrementActiveConnections(connType string)
	DecrementActiveConnections(connType string)
	RecordWarmPoolHit()
	Start() error
	Stop() error
}

// SandboxHandler defines the interface for the sandbox handler
type SandboxHandler interface {
	RegisterRoutes(router *gin.RouterGroup)
	HandleWebSocket(c *gin.Context)
}

// WarmPoolHandler defines the interface for the warm pool handler
type WarmPoolHandler interface {
	RegisterRoutes(router *gin.RouterGroup)
}

// RuntimeHandler defines the interface for the runtime handler
type RuntimeHandler interface {
	RegisterRoutes(router *gin.RouterGroup)
}

// ProfileHandler defines the interface for the profile handler
type ProfileHandler interface {
	RegisterRoutes(router *gin.RouterGroup)
}

// UserHandler defines the interface for the user handler
type UserHandler interface {
	RegisterRoutes(router *gin.RouterGroup)
}

// SandboxService defines the interface for sandbox services
type SandboxService interface {
	CreateSandbox(ctx context.Context, req types.CreateSandboxRequest) (*types.Sandbox, error)
	GetSandbox(ctx context.Context, sandboxID string) (*types.Sandbox, error)
	ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error)
	TerminateSandbox(ctx context.Context, sandboxID string) error
	GetSandboxStatus(ctx context.Context, sandboxID string) (*types.SandboxStatus, error)
	Execute(ctx context.Context, req types.ExecuteRequest) (*types.ExecutionResult, error)
	ListFiles(ctx context.Context, sandboxID, path string) ([]types.FileInfo, error)
	DownloadFile(ctx context.Context, sandboxID, path string) ([]byte, error)
	UploadFile(ctx context.Context, sandboxID, path string, content []byte) (*types.FileInfo, error)
	DeleteFile(ctx context.Context, sandboxID, path string) error
	InstallPackages(ctx context.Context, req types.InstallPackagesRequest) (*types.ExecutionResult, error)
	CreateSession(userID, sandboxID string, conn types.WSConnection) (*types.Session, error)
	CloseSession(sessionID string)
	HandleSession(session *types.Session)
	Start() error
	Stop() error
}

// WarmPoolService defines the interface for warm pool services
type WarmPoolService interface {
	GetWarmSandbox(ctx context.Context, runtime string) (string, error)
	AddToWarmPool(ctx context.Context, sandboxID, runtime string) error
	RemoveFromWarmPool(ctx context.Context, sandboxID string) error
	GetWarmPoolStatus(ctx context.Context, name, namespace string) (map[string]interface{}, error)
	GetGlobalWarmPoolStatus(ctx context.Context) (map[string]interface{}, error)
	CheckAvailability(ctx context.Context, runtime, securityLevel string) (bool, error)
	CreateWarmPool(ctx context.Context, req types.CreateWarmPoolRequest) (*types.WarmPool, error)
	GetWarmPool(ctx context.Context, name, namespace string) (*types.WarmPool, error)
	ListWarmPools(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error)
	UpdateWarmPool(ctx context.Context, req types.UpdateWarmPoolRequest) (*types.WarmPool, error)
	DeleteWarmPool(ctx context.Context, name, namespace string) error
	Start() error
	Stop() error
}

// Services defines the interface for accessing all application services
type Services interface {
	GetAuth() AuthService
	GetDatabase() DatabaseService
	GetCache() CacheService
	GetExecution() ExecutionService
	GetFile() FileService
	GetMetrics() MetricsService
	GetSandbox() SandboxService
	GetWarmPool() WarmPoolService
}

// Use the shared Kubernetes interfaces
type KubernetesClient = k8sinterfaces.KubernetesClient
type LLMSafespaceV1Interface = k8sinterfaces.LLMSafespaceV1Interface
type SandboxInterface = k8sinterfaces.SandboxInterface
type WarmPoolInterface = k8sinterfaces.WarmPoolInterface
type WarmPodInterface = k8sinterfaces.WarmPodInterface
type RuntimeEnvironmentInterface = k8sinterfaces.RuntimeEnvironmentInterface
type SandboxProfileInterface = k8sinterfaces.SandboxProfileInterface
