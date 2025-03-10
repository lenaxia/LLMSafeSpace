package interfaces

import (
    llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/watch"
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

// LLMSafespaceV1Interface defines the interface for LLMSafespace API group
type LLMSafespaceV1Interface interface {
    Sandboxes(namespace string) SandboxInterface
    WarmPools(namespace string) WarmPoolInterface
    WarmPods(namespace string) WarmPodInterface
    RuntimeEnvironments(namespace string) RuntimeEnvironmentInterface
    SandboxProfiles(namespace string) SandboxProfileInterface
}

// SandboxInterface defines the interface for Sandbox operations
type SandboxInterface interface {
    Create(*llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error)
    Update(*llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error)
    UpdateStatus(*llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error)
    Delete(name string, options metav1.DeleteOptions) error
    Get(name string, options metav1.GetOptions) (*llmsafespacev1.Sandbox, error)
    List(opts metav1.ListOptions) (*llmsafespacev1.SandboxList, error)
    Watch(opts metav1.ListOptions) (watch.Interface, error)
}

// WarmPoolInterface defines the interface for WarmPool operations
type WarmPoolInterface interface {
    Create(*llmsafespacev1.WarmPool) (*llmsafespacev1.WarmPool, error)
    Update(*llmsafespacev1.WarmPool) (*llmsafespacev1.WarmPool, error)
    UpdateStatus(*llmsafespacev1.WarmPool) (*llmsafespacev1.WarmPool, error)
    Delete(name string, options metav1.DeleteOptions) error
    Get(name string, options metav1.GetOptions) (*llmsafespacev1.WarmPool, error)
    List(opts metav1.ListOptions) (*llmsafespacev1.WarmPoolList, error)
    Watch(opts metav1.ListOptions) (watch.Interface, error)
}

// WarmPodInterface defines the interface for WarmPod operations
type WarmPodInterface interface {
    Create(*llmsafespacev1.WarmPod) (*llmsafespacev1.WarmPod, error)
    Update(*llmsafespacev1.WarmPod) (*llmsafespacev1.WarmPod, error)
    UpdateStatus(*llmsafespacev1.WarmPod) (*llmsafespacev1.WarmPod, error)
    Delete(name string, options metav1.DeleteOptions) error
    Get(name string, options metav1.GetOptions) (*llmsafespacev1.WarmPod, error)
    List(opts metav1.ListOptions) (*llmsafespacev1.WarmPodList, error)
    Watch(opts metav1.ListOptions) (watch.Interface, error)
}

// RuntimeEnvironmentInterface defines the interface for RuntimeEnvironment operations
type RuntimeEnvironmentInterface interface {
    Create(*llmsafespacev1.RuntimeEnvironment) (*llmsafespacev1.RuntimeEnvironment, error)
    Update(*llmsafespacev1.RuntimeEnvironment) (*llmsafespacev1.RuntimeEnvironment, error)
    UpdateStatus(*llmsafespacev1.RuntimeEnvironment) (*llmsafespacev1.RuntimeEnvironment, error)
    Delete(name string, options metav1.DeleteOptions) error
    Get(name string, options metav1.GetOptions) (*llmsafespacev1.RuntimeEnvironment, error)
    List(opts metav1.ListOptions) (*llmsafespacev1.RuntimeEnvironmentList, error)
    Watch(opts metav1.ListOptions) (watch.Interface, error)
}

// SandboxProfileInterface defines the interface for SandboxProfile operations
type SandboxProfileInterface interface {
    Create(*llmsafespacev1.SandboxProfile) (*llmsafespacev1.SandboxProfile, error)
    Update(*llmsafespacev1.SandboxProfile) (*llmsafespacev1.SandboxProfile, error)
    Delete(name string, options metav1.DeleteOptions) error
    Get(name string, options metav1.GetOptions) (*llmsafespacev1.SandboxProfile, error)
    List(opts metav1.ListOptions) (*llmsafespacev1.SandboxProfileList, error)
    Watch(opts metav1.ListOptions) (watch.Interface, error)
}
