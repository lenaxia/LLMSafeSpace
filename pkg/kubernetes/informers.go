// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package kubernetes

import (
	"context"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
)

type InformerFactory struct {
	client        interfaces.LLMSafespaceV1Interface
	defaultResync time.Duration
	namespace     string
	ctx           context.Context

	mu               sync.Mutex
	started          bool
	runtimeEnvInf    cache.SharedIndexInformer
	workspaceInf     cache.SharedIndexInformer
}

func NewInformerFactory(client interfaces.LLMSafespaceV1Interface, defaultResync time.Duration, namespace string) *InformerFactory {
	return &InformerFactory{
		client:        client,
		defaultResync: defaultResync,
		namespace:     namespace,
		ctx:           context.Background(),
	}
}

func (f *InformerFactory) newRuntimeEnvInformerLocked() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return f.client.RuntimeEnvironments().List(f.ctx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return f.client.RuntimeEnvironments().Watch(f.ctx, options)
			},
		},
		&v1.RuntimeEnvironment{},
		f.defaultResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

func (f *InformerFactory) newWorkspaceInformerLocked() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return f.client.Workspaces(f.namespace).List(f.ctx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return f.client.Workspaces(f.namespace).Watch(f.ctx, options)
			},
		},
		&v1.Workspace{},
		f.defaultResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

func (f *InformerFactory) RuntimeEnvironmentInformer() cache.SharedIndexInformer {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runtimeEnvInf != nil {
		return f.runtimeEnvInf
	}
	f.runtimeEnvInf = f.newRuntimeEnvInformerLocked()
	return f.runtimeEnvInf
}

func (f *InformerFactory) WorkspaceInformer() cache.SharedIndexInformer {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.workspaceInf != nil {
		return f.workspaceInf
	}
	f.workspaceInf = f.newWorkspaceInformerLocked()
	return f.workspaceInf
}

func (f *InformerFactory) StartInformers(stopCh <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.started {
		return
	}
	f.started = true
	if f.runtimeEnvInf == nil {
		f.runtimeEnvInf = f.newRuntimeEnvInformerLocked()
	}
	if f.workspaceInf == nil {
		f.workspaceInf = f.newWorkspaceInformerLocked()
	}
	go f.runtimeEnvInf.Run(stopCh)
	go f.workspaceInf.Run(stopCh)
}
