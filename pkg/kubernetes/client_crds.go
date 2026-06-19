// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package kubernetes

import (
	"context"
	"fmt"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
	"github.com/lenaxia/llmsafespaces/pkg/interfaces"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

func init() {
	schemeBuilder := runtime.NewSchemeBuilder(
		func(scheme *runtime.Scheme) error {
			scheme.AddKnownTypes(
				schema.GroupVersion{Group: "llmsafespaces.dev", Version: "v1"},
				&v1.RuntimeEnvironment{},
				&v1.RuntimeEnvironmentList{},
				&v1.Workspace{},
				&v1.WorkspaceList{},
				&v1.InferenceRelay{},
				&v1.InferenceRelayList{},
			)
			metav1.AddToGroupVersion(scheme, schema.GroupVersion{Group: "llmsafespaces.dev", Version: "v1"})
			return nil
		},
	)
	if err := schemeBuilder.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add LLMSafeSpaces types to scheme: %v", err))
	}
}

type LLMSafespacesV1Client struct {
	restClient rest.Interface
	client     interfaces.LLMSafespacesV1Interface
}

func NewLLMSafespacesV1Client(restClient rest.Interface) *LLMSafespacesV1Client {
	return &LLMSafespacesV1Client{
		restClient: restClient,
	}
}

func (c *LLMSafespacesV1Client) WithMockClient(mock interfaces.LLMSafespacesV1Interface) *LLMSafespacesV1Client {
	c.client = mock
	return c
}

var _ interfaces.LLMSafespacesV1Interface = &LLMSafespacesV1Client{}

func newLLMSafespacesV1Client(c *rest.Config) (*LLMSafespacesV1Client, error) {
	config := *c
	config.Timeout = 0
	config.GroupVersion = &schema.GroupVersion{Group: "llmsafespaces.dev", Version: "v1"}
	config.APIPath = "/apis"
	config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme).WithoutConversion()
	config.UserAgent = rest.DefaultKubernetesUserAgent()

	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, err
	}

	return &LLMSafespacesV1Client{restClient: client}, nil
}

func (c *LLMSafespacesV1Client) RuntimeEnvironments() interfaces.RuntimeEnvironmentInterface {
	return &runtimeEnvironments{client: c.restClient}
}

func (c *LLMSafespacesV1Client) Workspaces(namespace string) interfaces.WorkspaceInterface {
	return &workspaces{client: c.restClient, ns: namespace}
}

func (c *LLMSafespacesV1Client) InferenceRelays() interfaces.InferenceRelayInterface {
	return &inferenceRelays{client: c.restClient}
}

type runtimeEnvironments struct {
	client rest.Interface
}

func (r *runtimeEnvironments) Create(ctx context.Context, runtimeEnv *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	result := &v1.RuntimeEnvironment{}
	err := r.client.Post().
		Resource("runtimeenvironments").
		Body(runtimeEnv).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *runtimeEnvironments) Update(ctx context.Context, runtimeEnv *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	result := &v1.RuntimeEnvironment{}
	err := r.client.Put().
		Resource("runtimeenvironments").
		Name(runtimeEnv.Name).
		Body(runtimeEnv).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *runtimeEnvironments) UpdateStatus(ctx context.Context, runtimeEnv *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	result := &v1.RuntimeEnvironment{}
	err := r.client.Put().
		Resource("runtimeenvironments").
		Name(runtimeEnv.Name).
		SubResource("status").
		Body(runtimeEnv).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *runtimeEnvironments) Delete(ctx context.Context, name string, options metav1.DeleteOptions) error {
	return r.client.Delete().
		Resource("runtimeenvironments").
		Name(name).
		Body(&options).
		Do(ctx).
		Error()
}

func (r *runtimeEnvironments) Get(ctx context.Context, name string, options metav1.GetOptions) (*v1.RuntimeEnvironment, error) {
	result := &v1.RuntimeEnvironment{}
	err := r.client.Get().
		Resource("runtimeenvironments").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *runtimeEnvironments) List(ctx context.Context, opts metav1.ListOptions) (*v1.RuntimeEnvironmentList, error) {
	result := &v1.RuntimeEnvironmentList{}
	err := r.client.Get().
		Resource("runtimeenvironments").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *runtimeEnvironments) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return r.client.Get().
		Resource("runtimeenvironments").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(ctx)
}

type workspaces struct {
	client rest.Interface
	ns     string
}

func (w *workspaces) Create(ctx context.Context, workspace *v1.Workspace) (*v1.Workspace, error) {
	result := &v1.Workspace{}
	err := w.client.Post().
		Namespace(w.ns).
		Resource("workspaces").
		Body(workspace).
		Do(ctx).
		Into(result)
	return result, err
}

func (w *workspaces) Update(ctx context.Context, workspace *v1.Workspace) (*v1.Workspace, error) {
	result := &v1.Workspace{}
	err := w.client.Put().
		Namespace(w.ns).
		Resource("workspaces").
		Name(workspace.Name).
		Body(workspace).
		Do(ctx).
		Into(result)
	return result, err
}

func (w *workspaces) UpdateStatus(ctx context.Context, workspace *v1.Workspace) (*v1.Workspace, error) {
	result := &v1.Workspace{}
	err := w.client.Put().
		Namespace(w.ns).
		Resource("workspaces").
		Name(workspace.Name).
		SubResource("status").
		Body(workspace).
		Do(ctx).
		Into(result)
	return result, err
}

func (w *workspaces) Delete(ctx context.Context, name string, options metav1.DeleteOptions) error {
	return w.client.Delete().
		Namespace(w.ns).
		Resource("workspaces").
		Name(name).
		Body(&options).
		Do(ctx).
		Error()
}

func (w *workspaces) Get(ctx context.Context, name string, options metav1.GetOptions) (*v1.Workspace, error) {
	result := &v1.Workspace{}
	err := w.client.Get().
		Namespace(w.ns).
		Resource("workspaces").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return result, err
}

func (w *workspaces) List(ctx context.Context, opts metav1.ListOptions) (*v1.WorkspaceList, error) {
	result := &v1.WorkspaceList{}
	err := w.client.Get().
		Namespace(w.ns).
		Resource("workspaces").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return result, err
}

func (w *workspaces) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return w.client.Get().
		Namespace(w.ns).
		Resource("workspaces").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(ctx)
}

func (w *workspaces) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions) (*v1.Workspace, error) {
	result := &v1.Workspace{}
	err := w.client.Patch(pt).
		Namespace(w.ns).
		Resource("workspaces").
		Name(name).
		Body(data).
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return result, err
}

type inferenceRelays struct {
	client rest.Interface
}

func (r *inferenceRelays) Create(ctx context.Context, obj *v1.InferenceRelay) (*v1.InferenceRelay, error) {
	result := &v1.InferenceRelay{}
	err := r.client.Post().
		Resource("inferencerelays").
		Body(obj).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *inferenceRelays) Update(ctx context.Context, obj *v1.InferenceRelay) (*v1.InferenceRelay, error) {
	result := &v1.InferenceRelay{}
	err := r.client.Put().
		Resource("inferencerelays").
		Name(obj.Name).
		Body(obj).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *inferenceRelays) UpdateStatus(ctx context.Context, obj *v1.InferenceRelay) (*v1.InferenceRelay, error) {
	result := &v1.InferenceRelay{}
	err := r.client.Put().
		Resource("inferencerelays").
		Name(obj.Name).
		SubResource("status").
		Body(obj).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *inferenceRelays) Delete(ctx context.Context, name string, options metav1.DeleteOptions) error {
	return r.client.Delete().
		Resource("inferencerelays").
		Name(name).
		Body(&options).
		Do(ctx).
		Error()
}

func (r *inferenceRelays) Get(ctx context.Context, name string, options metav1.GetOptions) (*v1.InferenceRelay, error) {
	result := &v1.InferenceRelay{}
	err := r.client.Get().
		Resource("inferencerelays").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *inferenceRelays) List(ctx context.Context, opts metav1.ListOptions) (*v1.InferenceRelayList, error) {
	result := &v1.InferenceRelayList{}
	err := r.client.Get().
		Resource("inferencerelays").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return result, err
}

func (r *inferenceRelays) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return r.client.Get().
		Resource("inferencerelays").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch(ctx)
}
