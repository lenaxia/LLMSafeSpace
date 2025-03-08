package interfaces

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

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
	Start() error
	Stop() error
}

// CacheService defines the interface for cache services
type CacheService interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, expiration time.Duration) error
	Delete(ctx context.Context, key string) error
	Start() error
	Stop() error
}

// ExecutionService defines the interface for execution services
type ExecutionService interface {
	ExecuteCode(ctx context.Context, sandboxID, code string, timeout int) (interface{}, error)
	ExecuteCommand(ctx context.Context, sandboxID, command string, timeout int) (interface{}, error)
	Start() error
	Stop() error
}

// FileService defines the interface for file services
type FileService interface {
	ListFiles(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) (interface{}, error)
	DownloadFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) ([]byte, error)
	UploadFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string, content []byte) (interface{}, error)
	DeleteFile(ctx context.Context, sandbox *llmsafespacev1.Sandbox, path string) error
	Start() error
	Stop() error
}

// MetricsService defines the interface for metrics services
type MetricsService interface {
	RecordRequest(method, path string, status int, duration time.Duration, size int)
	RecordSandboxCreation(runtime string, warmPodUsed bool)
	RecordSandboxTermination(runtime string)
	RecordExecution(execType, runtime, status string, duration time.Duration)
	IncActiveConnections()
	DecActiveConnections()
	RecordWarmPoolHit()
	Start() error
	Stop() error
}

// SandboxService defines the interface for sandbox services
type SandboxService interface {
	Start() error
	Stop() error
}

// WarmPoolService defines the interface for warm pool services
type WarmPoolService interface {
	GetWarmSandbox(ctx context.Context, runtime string) (string, error)
	AddToWarmPool(ctx context.Context, sandboxID, runtime string) error
	RemoveFromWarmPool(ctx context.Context, sandboxID string) error
	GetWarmPoolStatus(ctx context.Context, name, namespace string) (interface{}, error)
	GetGlobalWarmPoolStatus(ctx context.Context) (map[string]interface{}, error)
	Start() error
	Stop() error
}

// KubernetesClient defines the interface for Kubernetes operations
type KubernetesClient interface {
	Start() error
	Stop()
	Clientset() k8s.Interface
	RESTConfig() *rest.Config
	LlmsafespaceV1() kubernetes.LLMSafespaceV1Interface
	ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *kubernetes.FileRequest) (*kubernetes.FileList, error)
	DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *kubernetes.FileRequest) ([]byte, error)
	UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *kubernetes.FileRequest) (*kubernetes.FileResult, error)
	DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *kubernetes.FileRequest) error
	ExecuteInSandbox(ctx context.Context, namespace, name string, execReq *kubernetes.ExecutionRequest) (*kubernetes.ExecutionResult, error)
	ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *kubernetes.ExecutionRequest, outputCallback func(stream, content string)) (*kubernetes.ExecutionResult, error)
}

// Services holds all application services
type Services struct {
	Auth      AuthService
	Database  DatabaseService
	Cache     CacheService
	Execution ExecutionService
	File      FileService
	Metrics   MetricsService
	Sandbox   SandboxService
	WarmPool  WarmPoolService
}
