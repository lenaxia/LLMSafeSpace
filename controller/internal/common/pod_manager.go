package common

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/lenaxia/llmsafespace/controller/internal/resources"
)

// PodManager handles pod creation and management
type PodManager struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// NewPodManager creates a new PodManager
func NewPodManager(client client.Client, scheme *runtime.Scheme) *PodManager {
	return &PodManager{
		Client: client,
		Scheme: scheme,
	}
}

// CreateSandboxPod creates a new pod for a sandbox
func (p *PodManager) CreateSandboxPod(ctx context.Context, sandbox *resources.Sandbox) (*corev1.Pod, error) {
	// Create a unique name for the pod
	podName := fmt.Sprintf("sandbox-%s", sandbox.Name)
	
	// Define labels and annotations
	labels := map[string]string{
		LabelApp:       "llmsafespace",
		LabelComponent: ComponentSandbox,
		LabelSandboxID: sandbox.Name,
		LabelRuntime:   sandbox.Spec.Runtime,
	}
	
	annotations := map[string]string{
		AnnotationCreatedBy: ControllerName,
		AnnotationSandboxID: sandbox.Name,
	}
	
	// Define the pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   sandbox.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "sandbox",
					Image: sandbox.Spec.Runtime,
					Ports: []corev1.ContainerPort{
						{
							Name:          "http",
							ContainerPort: 8080,
							Protocol:      corev1.ProtocolTCP,
						},
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/health",
								Port: intstr.FromInt(8080),
							},
						},
						InitialDelaySeconds: 5,
						PeriodSeconds:       10,
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/ready",
								Port: intstr.FromInt(8080),
							},
						},
						InitialDelaySeconds: 2,
						PeriodSeconds:       5,
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyAlways,
		},
	}
	
	// Configure resources if specified
	if sandbox.Spec.Resources != nil {
		// Configure resource limits and requests
		if sandbox.Spec.Resources.CPU != "" || sandbox.Spec.Resources.Memory != "" {
			pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
				Limits:   corev1.ResourceList{},
				Requests: corev1.ResourceList{},
			}
			
			// Add CPU limit if specified
			if sandbox.Spec.Resources.CPU != "" {
				quantity, err := resource.ParseQuantity(sandbox.Spec.Resources.CPU)
				if err == nil {
					pod.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU] = quantity
					pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = quantity
				}
			}
			
			// Add Memory limit if specified
			if sandbox.Spec.Resources.Memory != "" {
				quantity, err := resource.ParseQuantity(sandbox.Spec.Resources.Memory)
				if err == nil {
					pod.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory] = quantity
					pod.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = quantity
				}
			}
		}
	}
	
	// Configure security context if specified
	if sandbox.Spec.SecurityContext != nil {
		// Configure security context
		pod.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
			RunAsUser:                &sandbox.Spec.SecurityContext.RunAsUser,
			RunAsGroup:               &sandbox.Spec.SecurityContext.RunAsGroup,
			AllowPrivilegeEscalation: &[]bool{false}[0],
			ReadOnlyRootFilesystem:   &[]bool{true}[0],
		}
	}
	
	// Set the owner reference
	if err := controllerutil.SetControllerReference(sandbox, pod, p.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference on pod: %w", err)
	}
	
	// Create the pod
	if err := p.Client.Create(ctx, pod); err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}
	
	return pod, nil
}

// CreateWarmPodPod creates a new pod for a warm pod
func (p *PodManager) CreateWarmPodPod(ctx context.Context, warmPod *resources.WarmPod, warmPool *resources.WarmPool) (*corev1.Pod, error) {
	// Create a unique name for the pod
	podName := fmt.Sprintf("warm-%s", warmPod.Name)
	
	// Define labels and annotations
	labels := map[string]string{
		LabelApp:       "llmsafespace",
		LabelComponent: ComponentWarmPod,
		LabelPoolName:  warmPool.Name,
		LabelRuntime:   warmPool.Spec.Runtime,
		LabelStatus:    "ready",
	}
	
	annotations := map[string]string{
		AnnotationCreatedBy: ControllerName,
		AnnotationWarmPodID: warmPod.Name,
	}
	
	// Define the pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   warmPod.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "warmpod",
					Image: warmPool.Spec.Runtime,
					Ports: []corev1.ContainerPort{
						{
							Name:          "http",
							ContainerPort: 8080,
							Protocol:      corev1.ProtocolTCP,
						},
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/health",
								Port: intstr.FromInt(8080),
							},
						},
						InitialDelaySeconds: 5,
						PeriodSeconds:       10,
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/ready",
								Port: intstr.FromInt(8080),
							},
						},
						InitialDelaySeconds: 2,
						PeriodSeconds:       5,
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyAlways,
		},
	}
	
	// Configure resources if specified
	if warmPool.Spec.Resources != nil {
		// Configure resource limits and requests
	}
	
	// Set the owner reference
	if err := controllerutil.SetControllerReference(warmPod, pod, p.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference on pod: %w", err)
	}
	
	// Create the pod
	if err := p.Client.Create(ctx, pod); err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}
	
	return pod, nil
}

// RecyclePod recycles a pod for reuse
func (p *PodManager) RecyclePod(ctx context.Context, pod *corev1.Pod) error {
	// Clean up the pod environment
	// This would involve executing commands in the pod to clean up files, etc.
	
	// Update the pod labels and annotations
	podCopy := pod.DeepCopy()
	delete(podCopy.Labels, LabelSandboxID)
	
	// Add recycling annotation
	if podCopy.Annotations == nil {
		podCopy.Annotations = make(map[string]string)
	}
	podCopy.Annotations[AnnotationRecyclable] = "true"
	
	// Update the pod
	if err := p.Client.Update(ctx, podCopy); err != nil {
		return fmt.Errorf("failed to update pod: %w", err)
	}
	
	return nil
}
