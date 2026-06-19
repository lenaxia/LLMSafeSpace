// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package interfaces

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

type KubernetesClient interface {
	Start() error
	Stop()
	Clientset() kubernetes.Interface
	DynamicClient() dynamic.Interface
	RESTConfig() *rest.Config
	InformerFactory() informers.SharedInformerFactory
	LlmsafespacesV1() (LLMSafespacesV1Interface, error)
}

type LLMSafespacesV1Interface interface {
	RuntimeEnvironments() RuntimeEnvironmentInterface
	Workspaces(namespace string) WorkspaceInterface
	InferenceRelays() InferenceRelayInterface
}

type RuntimeEnvironmentInterface interface {
	Create(ctx context.Context, obj *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error)
	Update(ctx context.Context, obj *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error)
	UpdateStatus(ctx context.Context, obj *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error)
	Delete(ctx context.Context, name string, options metav1.DeleteOptions) error
	Get(ctx context.Context, name string, options metav1.GetOptions) (*v1.RuntimeEnvironment, error)
	List(ctx context.Context, opts metav1.ListOptions) (*v1.RuntimeEnvironmentList, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
}

type WorkspaceInterface interface {
	Create(ctx context.Context, obj *v1.Workspace) (*v1.Workspace, error)
	Update(ctx context.Context, obj *v1.Workspace) (*v1.Workspace, error)
	UpdateStatus(ctx context.Context, obj *v1.Workspace) (*v1.Workspace, error)
	Delete(ctx context.Context, name string, options metav1.DeleteOptions) error
	Get(ctx context.Context, name string, options metav1.GetOptions) (*v1.Workspace, error)
	List(ctx context.Context, opts metav1.ListOptions) (*v1.WorkspaceList, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions) (*v1.Workspace, error)
}

type InferenceRelayInterface interface {
	Create(ctx context.Context, obj *v1.InferenceRelay) (*v1.InferenceRelay, error)
	Update(ctx context.Context, obj *v1.InferenceRelay) (*v1.InferenceRelay, error)
	UpdateStatus(ctx context.Context, obj *v1.InferenceRelay) (*v1.InferenceRelay, error)
	Delete(ctx context.Context, name string, options metav1.DeleteOptions) error
	Get(ctx context.Context, name string, options metav1.GetOptions) (*v1.InferenceRelay, error)
	List(ctx context.Context, opts metav1.ListOptions) (*v1.InferenceRelayList, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
}
