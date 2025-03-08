package interfaces

import (
	"context"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubernetesClient defines the interface for Kubernetes operations
type KubernetesClient interface {
	Start() error
	Stop()
	Clientset() kubernetes.Interface
	RESTConfig() *rest.Config
	LlmsafespaceV1() LLMSafespaceV1Interface
	ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *FileRequest) (*FileList, error)
	DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *FileRequest) ([]byte, error)
	UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *FileRequest) (*FileResult, error)
	DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *FileRequest) error
	ExecuteInSandbox(ctx context.Context, namespace, name string, execReq *ExecutionRequest) (*ExecutionResult, error)
	ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *ExecutionRequest, outputCallback func(stream, content string)) (*ExecutionResult, error)
}

// FileRequest represents a file operation request
type FileRequest struct {
	Path    string
	Content []byte
}

// FileList represents a list of files
type FileList struct {
	Files []FileInfo
}

// FileResult represents the result of a file operation
type FileResult struct {
	Path      string
	Size      int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ExecutionRequest represents an execution request
type ExecutionRequest struct {
	Type    string
	Content string
	Timeout int
	Stream  bool
}

// ExecutionResult represents the result of an execution
type ExecutionResult struct {
	ID          string
	Status      string
	StartedAt   time.Time
	CompletedAt time.Time
	ExitCode    int
	Stdout      string
	Stderr      string
}

// LLMSafespaceV1Interface defines the interface for LLMSafespace v1 API operations
type LLMSafespaceV1Interface interface {
	Sandboxes(namespace string) SandboxInterface
}

// SandboxInterface defines the interface for sandbox operations
type SandboxInterface interface {
	Create(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error)
	Update(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error)
	UpdateStatus(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*llmsafespacev1.Sandbox, error)
	List(opts metav1.ListOptions) (*llmsafespacev1.SandboxList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}
