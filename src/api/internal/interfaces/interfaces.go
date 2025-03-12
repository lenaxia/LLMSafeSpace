package interfaces

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	
	"github.com/lenaxia/llmsafespace/api/internal/types"
)

// WSConnection defines the interface for a WebSocket connection
type WSConnection interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	WriteJSON(v interface{}) error
	Close() error
	SetWriteDeadline(t time.Time) error
}

// SessionManager defines the interface for managing WebSocket sessions
type SessionManager interface {
	CreateSession(userID, sandboxID string, conn WSConnection) (*types.Session, error)
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
	CreateSession(userID, sandboxID string, conn WSConnection) (*types.Session, error)
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

// KubernetesClient defines the interface for Kubernetes client operations
type KubernetesClient interface {
	Start() error
	Stop()
	Clientset() kubernetes.Interface
	RESTConfig() *rest.Config
	LlmsafespaceV1() LLMSafespaceV1Interface
	ExecuteInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest) (*types.ExecutionResult, error)
	ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest, outputCallback func(stream, content string)) (*types.ExecutionResult, error)
	ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileList, error)
	DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) ([]byte, error)
	UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileResult, error)
	DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) error
}

// LLMSafespaceV1Interface defines the interface for LLMSafespace v1 API operations
type LLMSafespaceV1Interface interface {
	Sandboxes(namespace string) SandboxInterface
	WarmPools(namespace string) WarmPoolInterface
	WarmPods(namespace string) WarmPodInterface
	RuntimeEnvironments(namespace string) RuntimeEnvironmentInterface
	SandboxProfiles(namespace string) SandboxProfileInterface
}

// SandboxInterface defines the interface for Sandbox operations
type SandboxInterface interface {
	Create(*types.Sandbox) (*types.Sandbox, error)
	Update(*types.Sandbox) (*types.Sandbox, error)
	UpdateStatus(*types.Sandbox) (*types.Sandbox, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*types.Sandbox, error)
	List(opts metav1.ListOptions) (*types.SandboxList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}

// WarmPoolInterface defines the interface for WarmPool operations
type WarmPoolInterface interface {
	Create(*types.WarmPool) (*types.WarmPool, error)
	Update(*types.WarmPool) (*types.WarmPool, error)
	UpdateStatus(*types.WarmPool) (*types.WarmPool, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*types.WarmPool, error)
	List(opts metav1.ListOptions) (*types.WarmPoolList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}

// WarmPodInterface defines the interface for WarmPod operations
type WarmPodInterface interface {
	Create(*types.WarmPod) (*types.WarmPod, error)
	Update(*types.WarmPod) (*types.WarmPod, error)
	UpdateStatus(*types.WarmPod) (*types.WarmPod, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*types.WarmPod, error)
	List(opts metav1.ListOptions) (*types.WarmPodList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}

// RuntimeEnvironmentInterface defines the interface for RuntimeEnvironment operations
type RuntimeEnvironmentInterface interface {
	Create(*types.RuntimeEnvironment) (*types.RuntimeEnvironment, error)
	Update(*types.RuntimeEnvironment) (*types.RuntimeEnvironment, error)
	UpdateStatus(*types.RuntimeEnvironment) (*types.RuntimeEnvironment, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*types.RuntimeEnvironment, error)
	List(opts metav1.ListOptions) (*types.RuntimeEnvironmentList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}

// SandboxProfileInterface defines the interface for SandboxProfile operations
type SandboxProfileInterface interface {
	Create(*types.SandboxProfile) (*types.SandboxProfile, error)
	Update(*types.SandboxProfile) (*types.SandboxProfile, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*types.SandboxProfile, error)
	List(opts metav1.ListOptions) (*types.SandboxProfileList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}
