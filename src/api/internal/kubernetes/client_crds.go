package kubernetes

import (
	"context"
	"fmt"

	"github.com/lenaxia/llmsafespace/api/internal/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// Initialize CRD scheme
func init() {
	schemeBuilder := runtime.NewSchemeBuilder(
		func(scheme *runtime.Scheme) error {
			scheme.AddKnownTypes(
				schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"},
				&types.Sandbox{},
				&types.SandboxList{},
				&types.WarmPool{},
				&types.WarmPoolList{},
				&types.WarmPod{},
				&types.WarmPodList{},
				&types.RuntimeEnvironment{},
				&types.RuntimeEnvironmentList{},
				&types.SandboxProfile{},
				&types.SandboxProfileList{},
			)
			metav1.AddToGroupVersion(scheme, schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"})
			return nil
		},
	)
	if err := schemeBuilder.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add LLMSafeSpace types to scheme: %v", err))
	}
}

// LLMSafespaceV1Client is a client for the llmsafespace.dev/v1 API group
type LLMSafespaceV1Client struct {
	restClient rest.Interface
}

var _ LLMSafespaceV1Interface = (*LLMSafespaceV1Client)(nil)

// SandboxesGetter defines the interface for getting Sandboxes
type SandboxesGetter interface {
	Sandboxes(namespace string) SandboxInterface
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

// WarmPoolsGetter defines the interface for getting WarmPools
type WarmPoolsGetter interface {
	WarmPools(namespace string) WarmPoolInterface
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

// warmPools implements WarmPoolInterface
type warmPools struct {
	client rest.Interface
	ns     string
}

// WarmPools returns a WarmPoolInterface for the given namespace
func (c *LLMSafespaceV1Client) WarmPools(namespace string) WarmPoolInterface {
	return &warmPools{
		client: c.restClient,
		ns:     namespace,
	}
}

// Create creates a new WarmPool
func (w *warmPools) Create(warmPool *types.WarmPool) (*types.WarmPool, error) {
	result := &types.WarmPool{}
	err := w.client.Post().
		Namespace(w.ns).
		Resource("warmpools").
		Body(warmPool).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Update updates an existing WarmPool
func (w *warmPools) Update(warmPool *types.WarmPool) (*types.WarmPool, error) {
	result := &types.WarmPool{}
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
func (w *warmPools) UpdateStatus(warmPool *types.WarmPool) (*types.WarmPool, error) {
	result := &types.WarmPool{}
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
func (w *warmPools) Get(name string, options metav1.GetOptions) (*types.WarmPool, error) {
	result := &types.WarmPool{}
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
func (w *warmPools) List(opts metav1.ListOptions) (*types.WarmPoolList, error) {
	result := &types.WarmPoolList{}
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
	WarmPods(namespace string) WarmPodInterface
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

// warmPods implements WarmPodInterface
type warmPods struct {
	client rest.Interface
	ns     string
}

// WarmPods returns a WarmPodInterface for the given namespace
func (c *LLMSafespaceV1Client) WarmPods(namespace string) WarmPodInterface {
	return &warmPods{
		client: c.restClient,
		ns:     namespace,
	}
}

// Create creates a new WarmPod
func (w *warmPods) Create(warmPod *types.WarmPod) (*types.WarmPod, error) {
	result := &types.WarmPod{}
	err := w.client.Post().
		Namespace(w.ns).
		Resource("warmpods").
		Body(warmPod).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Update updates an existing WarmPod
func (w *warmPods) Update(warmPod *types.WarmPod) (*types.WarmPod, error) {
	result := &types.WarmPod{}
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
func (w *warmPods) UpdateStatus(warmPod *types.WarmPod) (*types.WarmPod, error) {
	result := &types.WarmPod{}
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
func (w *warmPods) Get(name string, options metav1.GetOptions) (*types.WarmPod, error) {
	result := &types.WarmPod{}
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
func (w *warmPods) List(opts metav1.ListOptions) (*types.WarmPodList, error) {
	result := &types.WarmPodList{}
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
	RuntimeEnvironments(namespace string) RuntimeEnvironmentInterface
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

// runtimeEnvironments implements RuntimeEnvironmentInterface
type runtimeEnvironments struct {
	client rest.Interface
	ns     string
}

// RuntimeEnvironments returns a RuntimeEnvironmentInterface for the given namespace
func (c *LLMSafespaceV1Client) RuntimeEnvironments(namespace string) RuntimeEnvironmentInterface {
	return &runtimeEnvironments{
		client: c.restClient,
		ns:     namespace,
	}
}

// Create creates a new RuntimeEnvironment
func (r *runtimeEnvironments) Create(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	result := &types.RuntimeEnvironment{}
	err := r.client.Post().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Body(runtimeEnv).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Update updates an existing RuntimeEnvironment
func (r *runtimeEnvironments) Update(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	result := &types.RuntimeEnvironment{}
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
func (r *runtimeEnvironments) UpdateStatus(runtimeEnv *types.RuntimeEnvironment) (*types.RuntimeEnvironment, error) {
	result := &types.RuntimeEnvironment{}
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
func (r *runtimeEnvironments) Get(name string, options metav1.GetOptions) (*types.RuntimeEnvironment, error) {
	result := &types.RuntimeEnvironment{}
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
func (r *runtimeEnvironments) List(opts metav1.ListOptions) (*types.RuntimeEnvironmentList, error) {
	result := &types.RuntimeEnvironmentList{}
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
	SandboxProfiles(namespace string) SandboxProfileInterface
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

// sandboxProfiles implements SandboxProfileInterface
type sandboxProfiles struct {
	client rest.Interface
	ns     string
}

// SandboxProfiles returns a SandboxProfileInterface for the given namespace
func (c *LLMSafespaceV1Client) SandboxProfiles(namespace string) SandboxProfileInterface {
	return &sandboxProfiles{
		client: c.restClient,
		ns:     namespace,
	}
}

// Create creates a new SandboxProfile
func (s *sandboxProfiles) Create(profile *types.SandboxProfile) (*types.SandboxProfile, error) {
	result := &types.SandboxProfile{}
	err := s.client.Post().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		Body(profile).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Update updates an existing SandboxProfile
func (s *sandboxProfiles) Update(profile *types.SandboxProfile) (*types.SandboxProfile, error) {
	result := &types.SandboxProfile{}
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
func (s *sandboxProfiles) Get(name string, options metav1.GetOptions) (*types.SandboxProfile, error) {
	result := &types.SandboxProfile{}
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
func (s *sandboxProfiles) List(opts metav1.ListOptions) (*types.SandboxProfileList, error) {
	result := &types.SandboxProfileList{}
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
func (s *sandboxes) Create(sandbox *types.Sandbox) (*types.Sandbox, error) {
	result := &types.Sandbox{}
	err := s.client.Post().
		Namespace(s.ns).
		Resource("sandboxes").
		Body(sandbox).
		Do(context.TODO()).
		Into(result)
	return result, err
}

// Update updates an existing Sandbox
func (s *sandboxes) Update(sandbox *types.Sandbox) (*types.Sandbox, error) {
	result := &types.Sandbox{}
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
func (s *sandboxes) UpdateStatus(sandbox *types.Sandbox) (*types.Sandbox, error) {
	result := &types.Sandbox{}
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
func (s *sandboxes) Get(name string, options metav1.GetOptions) (*types.Sandbox, error) {
	result := &types.Sandbox{}
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
func (s *sandboxes) List(opts metav1.ListOptions) (*types.SandboxList, error) {
	result := &types.SandboxList{}
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
