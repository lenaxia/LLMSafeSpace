package interfaces

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

type KubernetesClient interface {
	Start() error
	Stop()
	Clientset() kubernetes.Interface
	DynamicClient() dynamic.Interface
	RESTConfig() *rest.Config
	InformerFactory() informers.SharedInformerFactory
	LlmsafespaceV1() LLMSafespaceV1Interface
}

type LLMSafespaceV1Interface interface {
	RuntimeEnvironments(namespace string) RuntimeEnvironmentInterface
	Workspaces(namespace string) WorkspaceInterface
}

type RuntimeEnvironmentInterface interface {
	Create(*v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error)
	Update(*v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error)
	UpdateStatus(*v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*v1.RuntimeEnvironment, error)
	List(opts metav1.ListOptions) (*v1.RuntimeEnvironmentList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}

type WorkspaceInterface interface {
	Create(*v1.Workspace) (*v1.Workspace, error)
	Update(*v1.Workspace) (*v1.Workspace, error)
	UpdateStatus(*v1.Workspace) (*v1.Workspace, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*v1.Workspace, error)
	List(opts metav1.ListOptions) (*v1.WorkspaceList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}
