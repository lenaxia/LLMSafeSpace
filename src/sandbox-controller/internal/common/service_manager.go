package common

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/resources"
)

// ServiceManager handles service creation and management
type ServiceManager struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// NewServiceManager creates a new ServiceManager
func NewServiceManager(client client.Client, scheme *runtime.Scheme) *ServiceManager {
	return &ServiceManager{
		Client: client,
		Scheme: scheme,
	}
}

// CreateSandboxService creates a new service for a sandbox
func (s *ServiceManager) CreateSandboxService(ctx context.Context, sandbox *resources.Sandbox, podName string) (*corev1.Service, error) {
	// Create a unique name for the service
	serviceName := fmt.Sprintf("sandbox-%s", sandbox.Name)
	
	// Define labels and selectors
	labels := map[string]string{
		LabelApp:       "llmsafespace",
		LabelComponent: ComponentSandbox,
		LabelSandboxID: sandbox.Name,
	}
	
	selectors := map[string]string{
		LabelApp:       "llmsafespace",
		LabelComponent: ComponentSandbox,
		LabelSandboxID: sandbox.Name,
	}
	
	// Define the service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: sandbox.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: selectors,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       80,
					TargetPort: intstr.FromInt(8080),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
	
	// Set the owner reference
	if err := controllerutil.SetControllerReference(sandbox, service, s.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference on service: %w", err)
	}
	
	// Create the service
	if err := s.Client.Create(ctx, service); err != nil {
		return nil, fmt.Errorf("failed to create service: %w", err)
	}
	
	return service, nil
}

// DeleteService deletes a service
func (s *ServiceManager) DeleteService(ctx context.Context, namespace, name string) error {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	
	if err := s.Client.Delete(ctx, service); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete service: %w", err)
		}
	}
	
	return nil
}
