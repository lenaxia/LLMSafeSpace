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
	Sandboxes(namespace string) SandboxInterface
	RuntimeEnvironments(namespace string) RuntimeEnvironmentInterface
	SandboxProfiles(namespace string) SandboxProfileInterface
}

type SandboxInterface interface {
	Create(*v1.Sandbox) (*v1.Sandbox, error)
	Update(*v1.Sandbox) (*v1.Sandbox, error)
	UpdateStatus(*v1.Sandbox) (*v1.Sandbox, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*v1.Sandbox, error)
	List(opts metav1.ListOptions) (*v1.SandboxList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
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

type SandboxProfileInterface interface {
	Create(*v1.SandboxProfile) (*v1.SandboxProfile, error)
	Update(*v1.SandboxProfile) (*v1.SandboxProfile, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*v1.SandboxProfile, error)
	List(opts metav1.ListOptions) (*v1.SandboxProfileList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}
