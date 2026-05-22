package kubernetes

import (
	"context"
	"fmt"

	"github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

func init() {
	schemeBuilder := runtime.NewSchemeBuilder(
		func(scheme *runtime.Scheme) error {
			scheme.AddKnownTypes(
				schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"},
				&v1.Sandbox{},
				&v1.SandboxList{},
				&v1.RuntimeEnvironment{},
				&v1.RuntimeEnvironmentList{},
				&v1.SandboxProfile{},
				&v1.SandboxProfileList{},
			)
			metav1.AddToGroupVersion(scheme, schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"})
			return nil
		},
	)
	if err := schemeBuilder.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add LLMSafeSpace types to scheme: %v", err))
	}
}

type LLMSafespaceV1Client struct {
	restClient rest.Interface
	client     interfaces.LLMSafespaceV1Interface
}

func NewLLMSafespaceV1Client(restClient rest.Interface) *LLMSafespaceV1Client {
	return &LLMSafespaceV1Client{
		restClient: restClient,
	}
}

func (c *LLMSafespaceV1Client) WithMockClient(mock interfaces.LLMSafespaceV1Interface) *LLMSafespaceV1Client {
	c.client = mock
	return c
}

var _ interfaces.LLMSafespaceV1Interface = &LLMSafespaceV1Client{}

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

func (c *LLMSafespaceV1Client) Sandboxes(namespace string) interfaces.SandboxInterface {
	return &sandboxes{client: c.restClient, ns: namespace}
}

func (c *LLMSafespaceV1Client) RuntimeEnvironments(namespace string) interfaces.RuntimeEnvironmentInterface {
	return &runtimeEnvironments{client: c.restClient, ns: namespace}
}

func (c *LLMSafespaceV1Client) SandboxProfiles(namespace string) interfaces.SandboxProfileInterface {
	return &sandboxProfiles{client: c.restClient, ns: namespace}
}

type sandboxes struct {
	client rest.Interface
	ns     string
}

func (s *sandboxes) Create(sandbox *v1.Sandbox) (*v1.Sandbox, error) {
	result := &v1.Sandbox{}
	err := s.client.Post().
		Namespace(s.ns).
		Resource("sandboxes").
		Body(sandbox).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (s *sandboxes) Update(sandbox *v1.Sandbox) (*v1.Sandbox, error) {
	result := &v1.Sandbox{}
	err := s.client.Put().
		Namespace(s.ns).
		Resource("sandboxes").
		Name(sandbox.Name).
		Body(sandbox).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (s *sandboxes) UpdateStatus(sandbox *v1.Sandbox) (*v1.Sandbox, error) {
	result := &v1.Sandbox{}
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

func (s *sandboxes) Delete(name string, options metav1.DeleteOptions) error {
	return s.client.Delete().
		Namespace(s.ns).
		Resource("sandboxes").
		Name(name).
		Body(&options).
		Do(context.TODO()).
		Error()
}

func (s *sandboxes) Get(name string, options metav1.GetOptions) (*v1.Sandbox, error) {
	result := &v1.Sandbox{}
	err := s.client.Get().
		Namespace(s.ns).
		Resource("sandboxes").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (s *sandboxes) List(opts metav1.ListOptions) (*v1.SandboxList, error) {
	result := &v1.SandboxList{}
	err := s.client.Get().
		Namespace(s.ns).
		Resource("sandboxes").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (s *sandboxes) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return s.client.Get().
		Namespace(s.ns).
		Resource("sandboxes").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(context.TODO())
}

type runtimeEnvironments struct {
	client rest.Interface
	ns     string
}

func (r *runtimeEnvironments) Create(runtimeEnv *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	result := &v1.RuntimeEnvironment{}
	err := r.client.Post().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Body(runtimeEnv).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (r *runtimeEnvironments) Update(runtimeEnv *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	result := &v1.RuntimeEnvironment{}
	err := r.client.Put().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Name(runtimeEnv.Name).
		Body(runtimeEnv).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (r *runtimeEnvironments) UpdateStatus(runtimeEnv *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	result := &v1.RuntimeEnvironment{}
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

func (r *runtimeEnvironments) Delete(name string, options metav1.DeleteOptions) error {
	return r.client.Delete().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Name(name).
		Body(&options).
		Do(context.TODO()).
		Error()
}

func (r *runtimeEnvironments) Get(name string, options metav1.GetOptions) (*v1.RuntimeEnvironment, error) {
	result := &v1.RuntimeEnvironment{}
	err := r.client.Get().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (r *runtimeEnvironments) List(opts metav1.ListOptions) (*v1.RuntimeEnvironmentList, error) {
	result := &v1.RuntimeEnvironmentList{}
	err := r.client.Get().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (r *runtimeEnvironments) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return r.client.Get().
		Namespace(r.ns).
		Resource("runtimeenvironments").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(context.TODO())
}

type sandboxProfiles struct {
	client rest.Interface
	ns     string
}

func (s *sandboxProfiles) Create(profile *v1.SandboxProfile) (*v1.SandboxProfile, error) {
	result := &v1.SandboxProfile{}
	err := s.client.Post().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		Body(profile).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (s *sandboxProfiles) Update(profile *v1.SandboxProfile) (*v1.SandboxProfile, error) {
	result := &v1.SandboxProfile{}
	err := s.client.Put().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		Name(profile.Name).
		Body(profile).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (s *sandboxProfiles) Delete(name string, options metav1.DeleteOptions) error {
	return s.client.Delete().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		Name(name).
		Body(&options).
		Do(context.TODO()).
		Error()
}

func (s *sandboxProfiles) Get(name string, options metav1.GetOptions) (*v1.SandboxProfile, error) {
	result := &v1.SandboxProfile{}
	err := s.client.Get().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (s *sandboxProfiles) List(opts metav1.ListOptions) (*v1.SandboxProfileList, error) {
	result := &v1.SandboxProfileList{}
	err := s.client.Get().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (s *sandboxProfiles) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return s.client.Get().
		Namespace(s.ns).
		Resource("sandboxprofiles").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(context.TODO())
}
