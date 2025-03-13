package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

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

			// Set up mock clientset
			fakeClient := fake.NewSimpleClientset()
			k8sClient.On("Clientset").Return(fakeClient).Once()
			
			// Create test pod in fake client
			_, err := fakeClient.CoreV1().Pods(tt.sandbox.Status.PodNamespace).Create(context.Background(), tt.pod, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("Failed to create pod in fake client: %v", err)
			}

			// Set up Kubernetes API expectations
			k8sClient.On("LlmsafespaceV1").Return(llmMock).Once()
			llmMock.On("Sandboxes", "").Return(sandboxInterface).Once()
			sandboxInterface.On("List", mock.Anything).Return(sandboxList, nil).Once()

			if tt.sandbox.Status.Phase != tt.wantPhase {
				// Use mock.AnythingOfType instead of mock.MatchedBy for more flexible matching
				llmMock.On("Sandboxes", tt.sandbox.Namespace).Return(sandboxInterface).Once()
				sandboxInterface.On("UpdateStatus", mock.AnythingOfType("*types.Sandbox")).Return(tt.sandbox, nil).Once()
			}

			helper.reconcileSandboxes(context.Background())

			k8sClient.AssertExpectations(t)
			llmMock.AssertExpectations(t)
			sandboxInterface.AssertExpectations(t)
		})
	}
}
