package kubernetes

import (
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
}

func NewInformerFactory(client interfaces.LLMSafespaceV1Interface, defaultResync time.Duration, namespace string) *InformerFactory {
	return &InformerFactory{
		client:        client,
		defaultResync: defaultResync,
		namespace:     namespace,
	}
}

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
		&v1.RuntimeEnvironment{},
		f.defaultResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

func (f *InformerFactory) WorkspaceInformer() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return f.client.Workspaces(f.namespace).List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return f.client.Workspaces(f.namespace).Watch(options)
			},
		},
		&v1.Workspace{},
		f.defaultResync,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

func (f *InformerFactory) StartInformers(stopCh <-chan struct{}) {
	informers := []cache.SharedIndexInformer{
		f.RuntimeEnvironmentInformer(),
		f.WorkspaceInformer(),
	}
	for _, informer := range informers {
		go informer.Run(stopCh)
	}
}
