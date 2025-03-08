package kubernetes

import (
	"context"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubernetesClient defines the interface for Kubernetes operations
type KubernetesClient interface {
	Start() error
	Stop()
	Clientset() k8s.Interface
	RESTConfig() *rest.Config
	LlmsafespaceV1() LLMSafespaceV1Interface
	ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *FileRequest) (*FileList, error)
	DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *FileRequest) ([]byte, error)
	UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *FileRequest) (*FileResult, error)
	DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *FileRequest) error
	ExecuteInSandbox(ctx context.Context, namespace, name string, execReq *ExecutionRequest) (*ExecutionResult, error)
	ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *ExecutionRequest, outputCallback func(stream, content string)) (*ExecutionResult, error)
}
