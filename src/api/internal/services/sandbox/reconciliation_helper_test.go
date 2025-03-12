package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
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
				},
				Status: types.SandboxStatus{
					Phase: "Creating",
					PodName: "test-pod",
					PodNamespace: "default",
				},
			},
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			wantPhase: "Running",
		},
		{
			name: "failed sandbox",
			sandbox: &types.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sandbox",
				},
				Status: types.SandboxStatus{
					Phase: "Creating",
					PodName: "test-pod",
					PodNamespace: "default",
				},
			},
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodFailed,
				},
			},
			wantPhase: "Failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup expectations
			sandboxList := &types.SandboxList{
				Items: []types.Sandbox{*tt.sandbox},
			}

			k8sClient.On("LlmsafespaceV1").Return(llmMock)
			llmMock.On("Sandboxes", "").Return(sandboxInterface)
			sandboxInterface.On("List", mock.Anything).Return(sandboxList, nil)

			k8sClient.On("Clientset").Return(k8sClient)
			k8sClient.On("CoreV1").Return(k8sClient)
			k8sClient.On("Pods", tt.sandbox.Status.PodNamespace).Return(k8sClient)
			k8sClient.On("Get", mock.Anything, tt.sandbox.Status.PodName, mock.Anything).Return(tt.pod, nil)

			if tt.sandbox.Status.Phase != tt.wantPhase {
				updatedSandbox := tt.sandbox.DeepCopy()
				updatedSandbox.Status.Phase = tt.wantPhase
				llmMock.On("Sandboxes", tt.sandbox.Namespace).Return(sandboxInterface)
				sandboxInterface.On("UpdateStatus", mock.MatchedBy(func(s *types.Sandbox) bool {
					return s.Status.Phase == tt.wantPhase
				})).Return(updatedSandbox, nil)
			}

			// Execute
			helper.reconcileSandboxes(context.Background())

			// Verify expectations
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
		wantUpdate bool
	}{
		{
			name: "expired sandbox",
			sandbox: &types.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sandbox",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup expectations
			if tt.wantUpdate {
				k8sClient.On("LlmsafespaceV1").Return(llmMock)
				llmMock.On("Sandboxes", tt.sandbox.Namespace).Return(sandboxInterface)
				sandboxInterface.On("UpdateStatus", mock.Anything).Return(tt.sandbox, nil)
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
