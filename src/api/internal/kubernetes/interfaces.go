package kubernetes

import (
	"context"
	"time"

	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// LLMSafespaceV1Interface defines the interface for LLMSafespace v1 API operations
type LLMSafespaceV1Interface interface {
	Sandboxes(namespace string) SandboxInterface
}

// SandboxInterface defines the interface for Sandbox operations
type SandboxInterface interface {
	Create(*llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error)
	Update(*llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error)
	UpdateStatus(*llmsafespacev1.Sandbox) (*llmsafespacev1.Sandbox, error)
	Delete(name string, options *metav1.DeleteOptions) error
	Get(name string, options metav1.GetOptions) (*llmsafespacev1.Sandbox, error)
	List(opts metav1.ListOptions) (*llmsafespacev1.SandboxList, error)
	Watch(opts metav1.ListOptions) (watch.Interface, error)
}
