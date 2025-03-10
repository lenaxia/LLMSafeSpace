package interfaces

import (
    "github.com/lenaxia/llmsafespace/api/internal/types"
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
    ListFilesInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileList, error)
    DownloadFileFromSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) ([]byte, error)
    UploadFileToSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) (*types.FileResult, error)
    DeleteFileInSandbox(ctx context.Context, namespace, name string, fileReq *types.FileRequest) error
    ExecuteInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest) (*types.ExecutionResult, error)
    ExecuteStreamInSandbox(ctx context.Context, namespace, name string, execReq *types.ExecutionRequest, outputCallback func(stream, content string)) (*types.ExecutionResult, error)
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
