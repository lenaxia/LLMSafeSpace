// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package kubernetes

import (
	"context"
	"sync"

	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// MockKubernetesClient mocks interfaces.KubernetesClient.
type MockKubernetesClient struct{ mock.Mock }

var _ interfaces.KubernetesClient = (*MockKubernetesClient)(nil)

func NewMockKubernetesClient() *MockKubernetesClient { return &MockKubernetesClient{} }

func (m *MockKubernetesClient) Start() error { return m.Called().Error(0) }
func (m *MockKubernetesClient) Stop()        { m.Called() }
func (m *MockKubernetesClient) Clientset() k8s.Interface {
	return m.Called().Get(0).(k8s.Interface)
}
func (m *MockKubernetesClient) DynamicClient() dynamic.Interface {
	return m.Called().Get(0).(dynamic.Interface)
}
func (m *MockKubernetesClient) RESTConfig() *rest.Config {
	return m.Called().Get(0).(*rest.Config)
}
func (m *MockKubernetesClient) InformerFactory() informers.SharedInformerFactory {
	v := m.Called().Get(0)
	if v == nil {
		return nil
	}
	return v.(informers.SharedInformerFactory)
}
func (m *MockKubernetesClient) LlmsafespaceV1() (interfaces.LLMSafespaceV1Interface, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(interfaces.LLMSafespaceV1Interface), args.Error(1)
}

// MockLLMSafespaceV1Interface mocks interfaces.LLMSafespaceV1Interface.
type MockLLMSafespaceV1Interface struct{ mock.Mock }

var _ interfaces.LLMSafespaceV1Interface = (*MockLLMSafespaceV1Interface)(nil)

func NewMockLLMSafespaceV1Interface() *MockLLMSafespaceV1Interface {
	return &MockLLMSafespaceV1Interface{}
}

func (m *MockLLMSafespaceV1Interface) RuntimeEnvironments() interfaces.RuntimeEnvironmentInterface {
	return m.Called().Get(0).(interfaces.RuntimeEnvironmentInterface)
}
func (m *MockLLMSafespaceV1Interface) Workspaces(ns string) interfaces.WorkspaceInterface {
	return m.Called(ns).Get(0).(interfaces.WorkspaceInterface)
}

func (m *MockLLMSafespaceV1Interface) InferenceRelays() interfaces.InferenceRelayInterface {
	return m.Called().Get(0).(interfaces.InferenceRelayInterface)
}

// MockRuntimeEnvironmentInterface mocks interfaces.RuntimeEnvironmentInterface.
type MockRuntimeEnvironmentInterface struct{ mock.Mock }

var _ interfaces.RuntimeEnvironmentInterface = (*MockRuntimeEnvironmentInterface)(nil)

func NewMockRuntimeEnvironmentInterface() *MockRuntimeEnvironmentInterface {
	return &MockRuntimeEnvironmentInterface{}
}

func (m *MockRuntimeEnvironmentInterface) Create(ctx context.Context, r *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	args := m.Called(ctx, r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironment), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) Update(ctx context.Context, r *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	args := m.Called(ctx, r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironment), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) UpdateStatus(ctx context.Context, r *v1.RuntimeEnvironment) (*v1.RuntimeEnvironment, error) {
	args := m.Called(ctx, r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironment), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	return m.Called(ctx, name, opts).Error(0)
}
func (m *MockRuntimeEnvironmentInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*v1.RuntimeEnvironment, error) {
	args := m.Called(ctx, name, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironment), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) List(ctx context.Context, opts metav1.ListOptions) (*v1.RuntimeEnvironmentList, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.RuntimeEnvironmentList), args.Error(1)
}
func (m *MockRuntimeEnvironmentInterface) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}

// MockWorkspaceInterface mocks interfaces.WorkspaceInterface.
type MockWorkspaceInterface struct{ mock.Mock }

var _ interfaces.WorkspaceInterface = (*MockWorkspaceInterface)(nil)

func NewMockWorkspaceInterface() *MockWorkspaceInterface { return &MockWorkspaceInterface{} }

func (m *MockWorkspaceInterface) Create(ctx context.Context, w *v1.Workspace) (*v1.Workspace, error) {
	args := m.Called(ctx, w)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Workspace), args.Error(1)
}
func (m *MockWorkspaceInterface) Update(ctx context.Context, w *v1.Workspace) (*v1.Workspace, error) {
	args := m.Called(ctx, w)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Workspace), args.Error(1)
}
func (m *MockWorkspaceInterface) UpdateStatus(ctx context.Context, w *v1.Workspace) (*v1.Workspace, error) {
	args := m.Called(ctx, w)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Workspace), args.Error(1)
}
func (m *MockWorkspaceInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	return m.Called(ctx, name, opts).Error(0)
}
func (m *MockWorkspaceInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*v1.Workspace, error) {
	args := m.Called(ctx, name, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Workspace), args.Error(1)
}
func (m *MockWorkspaceInterface) List(ctx context.Context, opts metav1.ListOptions) (*v1.WorkspaceList, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.WorkspaceList), args.Error(1)
}
func (m *MockWorkspaceInterface) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}
func (m *MockWorkspaceInterface) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions) (*v1.Workspace, error) {
	args := m.Called(ctx, name, pt, data, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Workspace), args.Error(1)
}

// MockWatch mocks watch.Interface.
type MockWatch struct {
	mock.Mock
	ch   chan watch.Event
	once sync.Once
}

var _ watch.Interface = (*MockWatch)(nil)

func NewMockWatch() *MockWatch {
	return &MockWatch{ch: make(chan watch.Event, 10)}
}

// Stop closes the event channel exactly once, satisfying the watch.Interface contract.
func (m *MockWatch) Stop() {
	m.Called()
	m.once.Do(func() { close(m.ch) })
}

func (m *MockWatch) ResultChan() <-chan watch.Event {
	return m.ch
}

func (m *MockWatch) SendEvent(t watch.EventType, obj runtime.Object) {
	m.ch <- watch.Event{Type: t, Object: obj}
}

// MockInferenceRelayInterface mocks interfaces.InferenceRelayInterface.
type MockInferenceRelayInterface struct{ mock.Mock }

var _ interfaces.InferenceRelayInterface = (*MockInferenceRelayInterface)(nil)

func NewMockInferenceRelayInterface() *MockInferenceRelayInterface {
	return &MockInferenceRelayInterface{}
}

func (m *MockInferenceRelayInterface) Create(ctx context.Context, r *v1.InferenceRelay) (*v1.InferenceRelay, error) {
	args := m.Called(ctx, r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.InferenceRelay), args.Error(1)
}

func (m *MockInferenceRelayInterface) Update(ctx context.Context, r *v1.InferenceRelay) (*v1.InferenceRelay, error) {
	args := m.Called(ctx, r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.InferenceRelay), args.Error(1)
}

func (m *MockInferenceRelayInterface) UpdateStatus(ctx context.Context, r *v1.InferenceRelay) (*v1.InferenceRelay, error) {
	args := m.Called(ctx, r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.InferenceRelay), args.Error(1)
}

func (m *MockInferenceRelayInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	return m.Called(ctx, name, opts).Error(0)
}

func (m *MockInferenceRelayInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*v1.InferenceRelay, error) {
	args := m.Called(ctx, name, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.InferenceRelay), args.Error(1)
}

func (m *MockInferenceRelayInterface) List(ctx context.Context, opts metav1.ListOptions) (*v1.InferenceRelayList, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.InferenceRelayList), args.Error(1)
}

func (m *MockInferenceRelayInterface) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(watch.Interface), args.Error(1)
}
