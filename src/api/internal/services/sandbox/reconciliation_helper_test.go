package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
)

func TestReconcileSandboxes(t *testing.T) {
	// Setup - create a real logger that prints to stdout
	logger, err := logger.New(false, "debug", "console")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	k8sClient := new(mocks.MockKubernetesClient)
	llmMock := new(mocks.MockLLMSafespaceV1Interface)
	sandboxInterface := new(mocks.MockSandboxInterface)

	helper := &ReconciliationHelper{
		k8sClient: k8sClient,
		logger:    logger,
	}

	// Test cases
	tests := []struct {
		name     string
		sandbox  *types.Sandbox
		pod      *corev1.Pod
		wantPhase string
	}{
		{
			name: "running sandbox",
			sandbox: &types.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sandbox",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-5 * time.Minute),
					},
				},
				Status: types.SandboxStatus{
					Phase: "Creating",
					PodName: "test-pod",
					PodNamespace: "default",
				},
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-5 * time.Minute),
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Ready: true,
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{
									StartedAt: metav1.Now(),
								},
							},
						},
					},
				},
			},
			wantPhase: "Running",
		},
		{
			name: "failed sandbox",
			sandbox: &types.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sandbox",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-5 * time.Minute),
					},
				},
				Status: types.SandboxStatus{
					Phase: "Creating",
					PodName: "test-pod",
					PodNamespace: "default",
				},
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-5 * time.Minute),
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodFailed,
				},
			},
			wantPhase: "Failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear previous mocks
			k8sClient.ExpectedCalls = nil
			llmMock.ExpectedCalls = nil
			sandboxInterface.ExpectedCalls = nil
			
			// Setup expectations
			sandboxList := &types.SandboxList{
				Items: []types.Sandbox{*tt.sandbox},
			}

			// Use empty string for listing all namespaces
			k8sClient.On("LlmsafespaceV1").Return(llmMock)
			llmMock.On("Sandboxes", "").Return(sandboxInterface)
			sandboxInterface.On("List", mock.Anything).Return(sandboxList, nil)

			k8sClient.On("Clientset").Return(k8sClient)
			k8sClient.On("CoreV1").Return(k8sClient)
			k8sClient.On("Pods", tt.sandbox.Status.PodNamespace).Return(k8sClient)
			k8sClient.On("Get", context.Background(), tt.sandbox.Status.PodName, mock.Anything).Return(tt.pod, nil)

			if tt.sandbox.Status.Phase != tt.wantPhase {
				updatedSandbox := tt.sandbox.DeepCopy()
				updatedSandbox.Status.Phase = tt.wantPhase
				
				if tt.wantPhase == "Running" {
					updatedSandbox.Status.Resources = &types.ResourceStatus{
						CPUUsage: "0.1",
						MemoryUsage: "256Mi",
					}
				}
				
				// Use namespace-specific call for updates
				llmMock.On("Sandboxes", tt.sandbox.Namespace).Return(sandboxInterface)
				sandboxInterface.On("UpdateStatus", mock.MatchedBy(func(s *types.Sandbox) bool {
					return s.Status.Phase == tt.wantPhase && 
					       s.Name == tt.sandbox.Name &&
					       s.Namespace == tt.sandbox.Namespace
				})).Return(updatedSandbox, nil)
			}

			helper.reconcileSandboxes(context.Background())

			k8sClient.AssertExpectations(t)
			llmMock.AssertExpectations(t)
			sandboxInterface.AssertExpectations(t)
		})
	}
}

func TestHandleSandboxReconciliation(t *testing.T) {
	// Setup - create a real logger that prints to stdout
	logger, err := logger.New(false, "debug", "console")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	k8sClient := new(mocks.MockKubernetesClient)
	llmMock := new(mocks.MockLLMSafespaceV1Interface)
	sandboxInterface := new(mocks.MockSandboxInterface)

	helper := &ReconciliationHelper{
		k8sClient: k8sClient,
		logger:    logger,
	}

	// Test cases
	tests := []struct {
		name    string
		sandbox *types.Sandbox
		pod     *corev1.Pod
		wantUpdate bool
	}{
		{
			name: "expired sandbox",
			sandbox: &types.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sandbox",
					Namespace: "default",
				},
				Spec: types.SandboxSpec{
					Timeout: 300,
				},
				Status: types.SandboxStatus{
					Phase: "Running",
					StartTime: &metav1.Time{
						Time: time.Now().Add(-time.Hour),
					},
				},
			},
			wantUpdate: true,
		},
		{
			name: "stuck sandbox",
			sandbox: &types.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sandbox",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-time.Hour),
					},
				},
				Status: types.SandboxStatus{
					Phase: "Creating",
				},
			},
			wantUpdate: true,
		},
		{
			name: "missing pod",
			sandbox: &types.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sandbox",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-5 * time.Minute),
					},
				},
				Status: types.SandboxStatus{
					Phase: "Running",
					PodName: "missing-pod",
					PodNamespace: "default",
				},
			},
			pod: nil,
			wantUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear previous mocks
			k8sClient.ExpectedCalls = nil
			llmMock.ExpectedCalls = nil
			sandboxInterface.ExpectedCalls = nil
			
			// Setup expectations
			if tt.wantUpdate {
				k8sClient.On("LlmsafespaceV1").Return(llmMock)
				llmMock.On("Sandboxes", tt.sandbox.Namespace).Return(sandboxInterface)
				
				// Use MatchedBy to match the sandbox being updated
				sandboxInterface.On("UpdateStatus", mock.MatchedBy(func(s *types.Sandbox) bool {
					return s.Name == tt.sandbox.Name && s.Namespace == tt.sandbox.Namespace
				})).Return(tt.sandbox, nil)
			}
			
			// Setup pod lookup if needed
			if tt.sandbox.Status.PodName != "" {
				k8sClient.On("Clientset").Return(k8sClient)
				k8sClient.On("CoreV1").Return(k8sClient)
				k8sClient.On("Pods", tt.sandbox.Status.PodNamespace).Return(k8sClient)
				
				var err error
				if tt.pod == nil {
					// Simulate pod not found using errors.StatusError
					err = &errors.StatusError{
						ErrStatus: metav1.Status{
							Status: metav1.StatusFailure,
							Reason: metav1.StatusReasonNotFound,
							Code:   404,
						},
					}
				}
				k8sClient.On("Get", context.Background(), tt.sandbox.Status.PodName, mock.Anything).Return(tt.pod, err)
			}

			// Execute
			helper.handleSandboxReconciliation(context.Background(), tt.sandbox)

			// Verify expectations
			k8sClient.AssertExpectations(t)
			if tt.wantUpdate {
				llmMock.AssertExpectations(t)
				sandboxInterface.AssertExpectations(t)
			}
		})
	}
}
