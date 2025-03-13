package kubernetes

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// InformerFactory creates informers for custom resources
type InformerFactory struct {
	client        *LLMSafespaceV1Client
	defaultResync time.Duration
	namespace     string
}

// NewInformerFactory creates a new informer factory
func NewInformerFactory(client *LLMSafespaceV1Client, defaultResync time.Duration, namespace string) *InformerFactory {
	return &InformerFactory{
		client:        client,
		defaultResync: defaultResync,
		namespace:     namespace,
	}
}

// SandboxInformer returns an informer for Sandboxes
func (f *InformerFactory) SandboxInformer() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return f.client.Sandboxes(f.namespace).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return f.client.Sandboxes(f.namespace).Watch(options)
			},
		},
		&types.Sandbox{},
		f.defaultResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

// WarmPoolInformer returns an informer for WarmPools
func (f *InformerFactory) WarmPoolInformer() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return f.client.WarmPools(f.namespace).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return f.client.WarmPools(f.namespace).Watch(options)
			},
		},
		&types.WarmPool{},
		f.defaultResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

// WarmPodInformer returns an informer for WarmPods
func (f *InformerFactory) WarmPodInformer() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return f.client.WarmPods(f.namespace).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return f.client.WarmPods(f.namespace).Watch(options)
			},
		},
		&types.WarmPod{},
		f.defaultResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

// RuntimeEnvironmentInformer returns an informer for RuntimeEnvironments
func (f *InformerFactory) RuntimeEnvironmentInformer() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return f.client.RuntimeEnvironments(f.namespace).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return f.client.RuntimeEnvironments(f.namespace).Watch(options)
			},
		},
		&types.RuntimeEnvironment{},
		f.defaultResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

// SandboxProfileInformer returns an informer for SandboxProfiles
func (f *InformerFactory) SandboxProfileInformer() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return f.client.SandboxProfiles(f.namespace).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return f.client.SandboxProfiles(f.namespace).Watch(options)
			},
		},
		&types.SandboxProfile{},
		f.defaultResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

// StartInformers starts all informers
func (f *InformerFactory) StartInformers(stopCh <-chan struct{}) {
	informers := []cache.SharedIndexInformer{
		f.SandboxInformer(),
		f.WarmPoolInformer(),
		f.WarmPodInformer(),
		f.RuntimeEnvironmentInformer(),
		f.SandboxProfileInformer(),
	}

	for _, informer := range informers {
		go informer.Run(stopCh)
	}
}
