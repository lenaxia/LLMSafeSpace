package kubernetes

import (
	"context"
	"fmt"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
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
				&v1.RuntimeEnvironment{},
				&v1.RuntimeEnvironmentList{},
				&v1.Workspace{},
				&v1.WorkspaceList{},
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
	// Strip the request timeout for the typed client. The base rest.Config
	// in pkg/kubernetes/client.go sets a 30s Timeout for unary REST calls,
	// but the same setting kills long-lived Watch streams (their HTTP
	// connection is closed at 30s and the watcher.ResultChan() is closed
	// with eventCount=0). Watch responses have their own server-side
	// timeoutSeconds; the client-side Timeout should be 0 (no timeout)
	config.Timeout = 0
	config.GroupVersion = &schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"}
	config.APIPath = "/apis"
	// WithoutConversion() is required for CRD types that don't define a
	// separate internal hub version. Without it, the rest client's watch
	// decoder calls DecoderToVersion(serializer, nil), which (with conversion
	// enabled) tries to convert to the internal version of the object's
	// group — and fails with "no kind ... is registered for the internal
	// version of group llmsafespace.dev". See client_test.go for the
	// regression test that locks in this requirement.
	config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme).WithoutConversion()
	config.UserAgent = rest.DefaultKubernetesUserAgent()

	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, err
	}

	return &LLMSafespaceV1Client{restClient: client}, nil
}

func (c *LLMSafespaceV1Client) RuntimeEnvironments(namespace string) interfaces.RuntimeEnvironmentInterface {
	return &runtimeEnvironments{client: c.restClient, ns: namespace}
}

func (c *LLMSafespaceV1Client) Workspaces(namespace string) interfaces.WorkspaceInterface {
	return &workspaces{client: c.restClient, ns: namespace}
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

type workspaces struct {
	client rest.Interface
	ns     string
}

func (w *workspaces) Create(workspace *v1.Workspace) (*v1.Workspace, error) {
	result := &v1.Workspace{}
	err := w.client.Post().
		Namespace(w.ns).
		Resource("workspaces").
		Body(workspace).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (w *workspaces) Update(workspace *v1.Workspace) (*v1.Workspace, error) {
	result := &v1.Workspace{}
	err := w.client.Put().
		Namespace(w.ns).
		Resource("workspaces").
		Name(workspace.Name).
		Body(workspace).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (w *workspaces) UpdateStatus(workspace *v1.Workspace) (*v1.Workspace, error) {
	result := &v1.Workspace{}
	err := w.client.Put().
		Namespace(w.ns).
		Resource("workspaces").
		Name(workspace.Name).
		SubResource("status").
		Body(workspace).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (w *workspaces) Delete(name string, options metav1.DeleteOptions) error {
	return w.client.Delete().
		Namespace(w.ns).
		Resource("workspaces").
		Name(name).
		Body(&options).
		Do(context.TODO()).
		Error()
}

func (w *workspaces) Get(name string, options metav1.GetOptions) (*v1.Workspace, error) {
	result := &v1.Workspace{}
	err := w.client.Get().
		Namespace(w.ns).
		Resource("workspaces").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (w *workspaces) List(opts metav1.ListOptions) (*v1.WorkspaceList, error) {
	result := &v1.WorkspaceList{}
	err := w.client.Get().
		Namespace(w.ns).
		Resource("workspaces").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(context.TODO()).
		Into(result)
	return result, err
}

func (w *workspaces) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return w.client.Get().
		Namespace(w.ns).
		Resource("workspaces").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(context.TODO())
}
