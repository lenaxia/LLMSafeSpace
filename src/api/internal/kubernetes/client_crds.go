package kubernetes

import (
	"context"
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
	if err := schemeBuilder.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add LLMSafeSpace types to scheme: %v", err))
	}
}

// For backward compatibility
type LLMSafespaceV1Client struct {
	restClient rest.Interface
}

var _ interfaces.LLMSafespaceV1Interface = (*LLMSafespaceV1Client)(nil)

// LLMSafespaceV1Client is a client for the llmsafespace.dev/v1 API group
type LLMSafespaceV1Client struct {
	restClient rest.Interface
}

// SandboxesGetter defines the interface for getting Sandboxes
type SandboxesGetter interface {
	Sandboxes(namespace string) interfaces.SandboxInterface
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
func (c *LLMSafespaceV1Client) Sandboxes(namespace string) interfaces.SandboxInterface {
	return &sandboxes{
		client: c.restClient,
		ns:     namespace,
	}
}

// WarmPoolsGetter defines the interface for getting WarmPools
type WarmPoolsGetter interface {
	WarmPools(namespace string) interfaces.WarmPoolInterface
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

// warmPools implements WarmPoolInterface
type warmPools struct {
	client rest.Interface
	ns     string
}

// WarmPools returns a WarmPoolInterface for the given namespace
func (c *LLMSafespaceV1Client) WarmPools(namespace string) interfaces.WarmPoolInterface {
	return &warmPools{
		client: c.restClient,
		ns:     namespace,
	}
}

// Create creates a new WarmPool
func (w *warmPools) Create(warmPool *llmsafespacev1.WarmPool) (*llmsafespacev1.WarmPool, error) {
	result := &llmsafespacev1.WarmPool{}
	err := w.client.Post().
		Namespace(w.ns).
		Resource("warmpools").
		Body(warmPool).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Update updates an existing WarmPool
func (w *warmPools) Update(warmPool *llmsafespacev1.WarmPool) (*llmsafespacev1.WarmPool, error) {
	result := &llmsafespacev1.WarmPool{}
	err := w.client.Put().
		Namespace(w.ns).
		Resource("warmpools").
		Name(warmPool.Name).
		Body(warmPool).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// UpdateStatus updates the status of an existing WarmPool
func (w *warmPools) UpdateStatus(warmPool *llmsafespacev1.WarmPool) (*llmsafespacev1.WarmPool, error) {
	result := &llmsafespacev1.WarmPool{}
	err := w.client.Put().
		Namespace(w.ns).
		Resource("warmpools").
		Name(warmPool.Name).
		SubResource("status").
		Body(warmPool).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Delete deletes a WarmPool
func (w *warmPools) Delete(name string, options metav1.DeleteOptions) error {
	return w.client.Delete().
		Namespace(w.ns).
		Resource("warmpools").
		Name(name).
		Body(&options).
		Do(context.TODO()).
		Error()
}

// Get retrieves a WarmPool
func (w *warmPools) Get(name string, options metav1.GetOptions) (*llmsafespacev1.WarmPool, error) {
	result := &llmsafespacev1.WarmPool{}
	err := w.client.Get().
		Namespace(w.ns).
		Resource("warmpools").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// List lists all WarmPools in the namespace
func (w *warmPools) List(opts metav1.ListOptions) (*llmsafespacev1.WarmPoolList, error) {
	result := &llmsafespacev1.WarmPoolList{}
	err := w.client.Get().
		Namespace(w.ns).
		Resource("warmpools").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Watch returns a watch.Interface that watches the requested warmpools
func (w *warmPools) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return w.client.Get().
		Namespace(w.ns).
		Resource("warmpools").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(context.TODO())
}

// WarmPodsGetter defines the interface for getting WarmPods
type WarmPodsGetter interface {
	WarmPods(namespace string) interfaces.WarmPodInterface
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

// warmPods implements WarmPodInterface
type warmPods struct {
	client rest.Interface
	ns     string
}

// WarmPods returns a WarmPodInterface for the given namespace
func (c *LLMSafespaceV1Client) WarmPods(namespace string) interfaces.WarmPodInterface {
	return &warmPods{
		client: c.restClient,
		ns:     namespace,
	}
}

// Create creates a new WarmPod
func (w *warmPods) Create(warmPod *llmsafespacev1.WarmPod) (*llmsafespacev1.WarmPod, error) {
	result := &llmsafespacev1.WarmPod{}
	err := w.client.Post().
		Namespace(w.ns).
		Resource("warmpods").
		Body(warmPod).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Update updates an existing WarmPod
func (w *warmPods) Update(warmPod *llmsafespacev1.WarmPod) (*llmsafespacev1.WarmPod, error) {
	result := &llmsafespacev1.WarmPod{}
	err := w.client.Put().
		Namespace(w.ns).
		Resource("warmpods").
		Name(warmPod.Name).
		Body(warmPod).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// UpdateStatus updates the status of an existing WarmPod
func (w *warmPods) UpdateStatus(warmPod *llmsafespacev1.WarmPod) (*llmsafespacev1.WarmPod, error) {
	result := &llmsafespacev1.WarmPod{}
	err := w.client.Put().
		Namespace(w.ns).
		Resource("warmpods").
		Name(warmPod.Name).
		SubResource("status").
		Body(warmPod).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Delete deletes a WarmPod
func (w *warmPods) Delete(name string, options metav1.DeleteOptions) error {
	return w.client.Delete().
		Namespace(w.ns).
		Resource("warmpods").
		Name(name).
		Body(&options).
		Do(context.TODO()).
		Error()
}

// Get retrieves a WarmPod
func (w *warmPods) Get(name string, options metav1.GetOptions) (*llmsafespacev1.WarmPod, error) {
	result := &llmsafespacev1.WarmPod{}
	err := w.client.Get().
		Namespace(w.ns).
		Resource("warmpods").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// List lists all WarmPods in the namespace
func (w *warmPods) List(opts metav1.ListOptions) (*llmsafespacev1.WarmPodList, error) {
	result := &llmsafespacev1.WarmPodList{}
	err := w.client.Get().
		Namespace(w.ns).
		Resource("warmpods").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Watch returns a watch.Interface that watches the requested warmpods
func (w *warmPods) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return w.client.Get().
		Namespace(w.ns).
		Resource("warmpods").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(context.TODO())
}

// RuntimeEnvironmentsGetter defines the interface for getting RuntimeEnvironments
type RuntimeEnvironmentsGetter interface {
	RuntimeEnvironments(namespace string) interfaces.RuntimeEnvironmentInterface
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

// runtimeEnvironments implements RuntimeEnvironmentInterface
type runtimeEnvironments struct {
	client rest.Interface
	ns     string
}

// RuntimeEnvironments returns a RuntimeEnvironmentInterface for the given namespace
func (c *LLMSafespaceV1Client) RuntimeEnvironments(namespace string) interfaces.RuntimeEnvironmentInterface {
	return &runtimeEnvironments{
		client: c.restClient,
		ns:     namespace,
	}
}

// Create creates a new RuntimeEnvironment
func (r *runtimeEnvironments) Create(runtimeEnv *llmsafespacev1.RuntimeEnvironment) (*llmsafespacev1.RuntimeEnvironment, error) {
	result := &llmsafespacev1.RuntimeEnvironment{}
	err := r.client.Post().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Body(runtimeEnv).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Update updates an existing RuntimeEnvironment
func (r *runtimeEnvironments) Update(runtimeEnv *llmsafespacev1.RuntimeEnvironment) (*llmsafespacev1.RuntimeEnvironment, error) {
	result := &llmsafespacev1.RuntimeEnvironment{}
	err := r.client.Put().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Name(runtimeEnv.Name).
		Body(runtimeEnv).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// UpdateStatus updates the status of an existing RuntimeEnvironment
func (r *runtimeEnvironments) UpdateStatus(runtimeEnv *llmsafespacev1.RuntimeEnvironment) (*llmsafespacev1.RuntimeEnvironment, error) {
	result := &llmsafespacev1.RuntimeEnvironment{}
	err := r.client.Put().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Name(runtimeEnv.Name).
		SubResource("status").
		Body(runtimeEnv).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Delete deletes a RuntimeEnvironment
func (r *runtimeEnvironments) Delete(name string, options metav1.DeleteOptions) error {
	return r.client.Delete().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Name(name).
		Body(&options).
		Do(context.TODO()).
		Error()
}

// Get retrieves a RuntimeEnvironment
func (r *runtimeEnvironments) Get(name string, options metav1.GetOptions) (*llmsafespacev1.RuntimeEnvironment, error) {
	result := &llmsafespacev1.RuntimeEnvironment{}
	err := r.client.Get().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// List lists all RuntimeEnvironments in the namespace
func (r *runtimeEnvironments) List(opts metav1.ListOptions) (*llmsafespacev1.RuntimeEnvironmentList, error) {
	result := &llmsafespacev1.RuntimeEnvironmentList{}
	err := r.client.Get().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Watch returns a watch.Interface that watches the requested runtimeenvironments
func (r *runtimeEnvironments) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return r.client.Get().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(context.TODO())
}

// SandboxProfilesGetter defines the interface for getting SandboxProfiles
type SandboxProfilesGetter interface {
	SandboxProfiles(namespace string) interfaces.SandboxProfileInterface
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

// sandboxProfiles implements SandboxProfileInterface
type sandboxProfiles struct {
	client rest.Interface
	ns     string
}

// SandboxProfiles returns a SandboxProfileInterface for the given namespace
func (c *LLMSafespaceV1Client) SandboxProfiles(namespace string) interfaces.SandboxProfileInterface {
	return &sandboxProfiles{
		client: c.restClient,
		ns:     namespace,
	}
}

// Create creates a new SandboxProfile
func (s *sandboxProfiles) Create(profile *llmsafespacev1.SandboxProfile) (*llmsafespacev1.SandboxProfile, error) {
	result := &llmsafespacev1.SandboxProfile{}
	err := s.client.Post().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		Body(profile).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Update updates an existing SandboxProfile
func (s *sandboxProfiles) Update(profile *llmsafespacev1.SandboxProfile) (*llmsafespacev1.SandboxProfile, error) {
	result := &llmsafespacev1.SandboxProfile{}
	err := s.client.Put().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		Name(profile.Name).
		Body(profile).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Delete deletes a SandboxProfile
func (s *sandboxProfiles) Delete(name string, options metav1.DeleteOptions) error {
	return s.client.Delete().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		Name(name).
		Body(&options).
		Do(context.TODO()).
		Error()
}

// Get retrieves a SandboxProfile
func (s *sandboxProfiles) Get(name string, options metav1.GetOptions) (*llmsafespacev1.SandboxProfile, error) {
	result := &llmsafespacev1.SandboxProfile{}
	err := s.client.Get().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// List lists all SandboxProfiles in the namespace
func (s *sandboxProfiles) List(opts metav1.ListOptions) (*llmsafespacev1.SandboxProfileList, error) {
	result := &llmsafespacev1.SandboxProfileList{}
	err := s.client.Get().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Watch returns a watch.Interface that watches the requested sandboxprofiles
func (s *sandboxProfiles) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return s.client.Get().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(context.TODO())
}

// Create creates a new Sandbox
func (s *sandboxes) Create(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error) {
	result := &llmsafespacev1.Sandbox{}
	err := s.client.Post().
		Namespace(s.ns).
		Resource("sandboxes").
		Body(sandbox).
		Do(context.TODO()).
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
		Do(context.TODO()).
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
		Do(context.TODO()).
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
		Do(context.TODO()).
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
		Do(context.TODO()).
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
		Do(context.TODO()).
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
		Watch(context.TODO())
}

// Similar interfaces would be implemented for WarmPool, WarmPod, RuntimeEnvironment, and SandboxProfile
