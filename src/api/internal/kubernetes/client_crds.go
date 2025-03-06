package kubernetes

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	// Import custom resource definitions
	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// Initialize CRD scheme
func init() {
	// Add custom resource types to the default scheme
	schemeBuilder := runtime.NewSchemeBuilder(
		func(scheme *runtime.Scheme) error {
			scheme.AddKnownTypes(
				schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"},
				&llmsafespacev1.Sandbox{},
				&llmsafespacev1.SandboxList{},
				&llmsafespacev1.WarmPool{},
				&llmsafespacev1.WarmPoolList{},
				&llmsafespacev1.WarmPod{},
				&llmsafespacev1.WarmPodList{},
				&llmsafespacev1.RuntimeEnvironment{},
				&llmsafespacev1.RuntimeEnvironmentList{},
				&llmsafespacev1.SandboxProfile{},
				&llmsafespacev1.SandboxProfileList{},
			)
			metav1.AddToGroupVersion(scheme, schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"})
			return nil
		},
	)
	schemeBuilder.AddToScheme(scheme.Scheme)
}

// LLMSafespaceV1Client is a client for the llmsafespace.dev/v1 API group
type LLMSafespaceV1Client struct {
	restClient rest.Interface
}

// SandboxesGetter defines the interface for getting Sandboxes
type SandboxesGetter interface {
	Sandboxes(namespace string) SandboxInterface
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

// sandboxes implements SandboxInterface
type sandboxes struct {
	client rest.Interface
	ns     string
}

// newLLMSafespaceV1Client creates a new client for the llmsafespace.dev/v1 API group
func newLLMSafespaceV1Client(c *rest.Config) (*LLMSafespaceV1Client, error) {
	config := *c
	config.ContentConfig.GroupVersion = &schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"}
	config.APIPath = "/apis"
	config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme)
	config.UserAgent = rest.DefaultKubernetesUserAgent()

	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, err
	}

	return &LLMSafespaceV1Client{restClient: client}, nil
}

// Sandboxes returns a SandboxInterface for the given namespace
func (c *LLMSafespaceV1Client) Sandboxes(namespace string) SandboxInterface {
	return &sandboxes{
		client: c.restClient,
		ns:     namespace,
	}
}

// Create creates a new Sandbox
func (s *sandboxes) Create(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error) {
	result := &llmsafespacev1.Sandbox{}
	err := s.client.Post().
		Namespace(s.ns).
		Resource("sandboxes").
		Body(sandbox).
		Do().
		Into(result)
	return result, err
}

// Update updates an existing Sandbox
func (s *sandboxes) Update(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error) {
	result := &llmsafespacev1.Sandbox{}
	err := s.client.Put().
		Namespace(s.ns).
		Resource("sandboxes").
		Name(sandbox.Name).
		Body(sandbox).
		Do().
		Into(result)
	return result, err
}

// UpdateStatus updates the status of an existing Sandbox
func (s *sandboxes) UpdateStatus(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error) {
	result := &llmsafespacev1.Sandbox{}
	err := s.client.Put().
		Namespace(s.ns).
		Resource("sandboxes").
		Name(sandbox.Name).
		SubResource("status").
		Body(sandbox).
		Do().
		Into(result)
	return result, err
}

// Delete deletes a Sandbox
func (s *sandboxes) Delete(name string, options metav1.DeleteOptions) error {
	return s.client.Delete().
		Namespace(s.ns).
		Resource("sandboxes").
		Name(name).
		Body(&options).
		Do().
		Error()
}

// Get retrieves a Sandbox
func (s *sandboxes) Get(name string, options metav1.GetOptions) (*llmsafespacev1.Sandbox, error) {
	result := &llmsafespacev1.Sandbox{}
	err := s.client.Get().
		Namespace(s.ns).
		Resource("sandboxes").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do().
		Into(result)
	return result, err
}

// List lists all Sandboxes in the namespace
func (s *sandboxes) List(opts metav1.ListOptions) (*llmsafespacev1.SandboxList, error) {
	result := &llmsafespacev1.SandboxList{}
	err := s.client.Get().
		Namespace(s.ns).
		Resource("sandboxes").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do().
		Into(result)
	return result, err
}

// Watch returns a watch.Interface that watches the requested sandboxes
func (s *sandboxes) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return s.client.Get().
		Namespace(s.ns).
		Resource("sandboxes").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch()
}

// Similar interfaces would be implemented for WarmPool, WarmPod, RuntimeEnvironment, and SandboxProfile
