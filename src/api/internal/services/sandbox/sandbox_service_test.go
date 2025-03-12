package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/lenaxia/llmsafespace/api/internal/mocks"
)

func TestCreateSandbox(t *testing.T) {
	// Setup - create a real logger that prints to stdout
	logger, err := logger.New(false, "debug", "console")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	k8sClient := new(mocks.MockKubernetesClient)
	dbService := new(mocks.MockDatabaseService)
	warmPoolSvc := new(mocks.MockWarmPoolService)
	execSvc := new(mocks.MockExecutionService)
	fileSvc := new(mocks.MockFileService)
	metricsRecorder := new(mocks.MockMetricsRecorder)
	sessionMgr := new(mocks.MockSessionManager)

	svc := &service{
		logger:      logger,
		k8sClient:   k8sClient,
		dbService:   dbService,
		warmPoolSvc: warmPoolSvc,
		executionSvc: execSvc,
		fileSvc:     fileSvc,
		metrics:     metricsRecorder,
		sessionMgr:  sessionMgr,
	}

	// Test cases
	tests := []struct {
		name    string
		req     types.CreateSandboxRequest
		wantErr bool
	}{
		{
			name: "successful creation",
			req: types.CreateSandboxRequest{
				Runtime:       "python:3.10",
				SecurityLevel: "standard",
				Timeout:      300,
				UserID:       "test-user",
				Namespace:    "default",
			},
			wantErr: false,
		},
		{
			name: "invalid runtime",
			req: types.CreateSandboxRequest{
				Runtime:    "",
				UserID:    "test-user",
				Namespace: "default",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a context for the test
			ctx := context.Background()
			
			// Setup expectations
			if !tt.wantErr {
				sandbox := &types.Sandbox{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sandbox",
						Namespace: tt.req.Namespace,
					},
					Spec: types.SandboxSpec{
						Runtime: tt.req.Runtime,
					},
				}

				k8sClient.On("LlmsafespaceV1").Return(k8sClient)
				k8sClient.On("Sandboxes", tt.req.Namespace).Return(k8sClient)
				k8sClient.On("Create", mock.Anything).Return(sandbox, nil)

				dbService.On("CreateSandboxMetadata", 
					ctx, 
					"test-sandbox", 
					tt.req.UserID, 
					tt.req.Runtime,
				).Return(nil)

				metricsRecorder.On("RecordSandboxCreation", tt.req.Runtime, false).Return()
				metricsRecorder.On("RecordOperationDuration", "create", mock.Anything).Return()
			}

			// Execute
			got, err := svc.CreateSandbox(ctx, tt.req)

			// Assert
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, tt.req.Runtime, got.Spec.Runtime)
			assert.Equal(t, tt.req.Namespace, got.Namespace)

			// Verify expectations
			k8sClient.AssertExpectations(t)
			dbService.AssertExpectations(t)
			metricsRecorder.AssertExpectations(t)
		})
	}
}

func TestGetSandbox(t *testing.T) {
	// Setup - create a real logger that prints to stdout
	logger, err := logger.New(false, "debug", "console")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	k8sClient := new(mocks.MockKubernetesClient)
	dbService := new(mocks.MockDatabaseService)
	warmPoolSvc := new(mocks.MockWarmPoolService)
	execSvc := new(mocks.MockExecutionService)
	fileSvc := new(mocks.MockFileService)
	metricsRecorder := new(mocks.MockMetricsRecorder)
	sessionMgr := new(mocks.MockSessionManager)

	svc := &service{
		logger:      logger,
		k8sClient:   k8sClient,
		dbService:   dbService,
		warmPoolSvc: warmPoolSvc,
		executionSvc: execSvc,
		fileSvc:     fileSvc,
		metrics:     metricsRecorder,
		sessionMgr:  sessionMgr,
	}

	// Create a context for the test
	ctx := context.Background()

	// Test cases
	tests := []struct {
		name      string
		sandboxID string
		wantErr   bool
	}{
		{
			name:      "existing sandbox",
			sandboxID: "test-sandbox",
			wantErr:   false,
		},
		{
			name:      "non-existent sandbox",
			sandboxID: "missing-sandbox",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup expectations
			if !tt.wantErr {
				sandbox := &types.Sandbox{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.sandboxID,
					},
				}

				k8sClient.On("LlmsafespaceV1").Return(k8sClient)
				k8sClient.On("Sandboxes", "").Return(k8sClient)
				k8sClient.On("Get", tt.sandboxID, mock.Anything).Return(sandbox, nil)
			}

			// Execute
			got, err := svc.GetSandbox(ctx, tt.sandboxID)

			// Assert
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, tt.sandboxID, got.Name)

			// Verify expectations
			k8sClient.AssertExpectations(t)
		})
	}
}

func TestTerminateSandbox(t *testing.T) {
	// Setup - create a real logger that prints to stdout
	logger, err := logger.New(false, "debug", "console")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	k8sClient := new(mocks.MockKubernetesClient)
	dbService := new(mocks.MockDatabaseService)
	warmPoolSvc := new(mocks.MockWarmPoolService)
	execSvc := new(mocks.MockExecutionService)
	fileSvc := new(mocks.MockFileService)
	metricsRecorder := new(mocks.MockMetricsRecorder)
	sessionMgr := new(mocks.MockSessionManager)

	svc := &service{
		logger:      logger,
		k8sClient:   k8sClient,
		dbService:   dbService,
		warmPoolSvc: warmPoolSvc,
		executionSvc: execSvc,
		fileSvc:     fileSvc,
		metrics:     metricsRecorder,
		sessionMgr:  sessionMgr,
	}

	// Create a context for the test
	ctx := context.Background()

	// Test cases
	tests := []struct {
		name      string
		sandboxID string
		wantErr   bool
	}{
		{
			name:      "successful termination",
			sandboxID: "test-sandbox",
			wantErr:   false,
		},
		{
			name:      "non-existent sandbox",
			sandboxID: "missing-sandbox",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup expectations
			if !tt.wantErr {
				sandbox := &types.Sandbox{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tt.sandboxID,
						Namespace: "default",
					},
					Spec: types.SandboxSpec{
						Runtime: "python:3.10",
					},
				}

				k8sClient.On("LlmsafespaceV1").Return(k8sClient)
				k8sClient.On("Sandboxes", "").Return(k8sClient)
				k8sClient.On("Get", tt.sandboxID, mock.Anything).Return(sandbox, nil)
				k8sClient.On("Delete", tt.sandboxID, mock.Anything).Return(nil)

				metricsRecorder.On("RecordSandboxTermination", "python:3.10").Return()
				metricsRecorder.On("RecordOperationDuration", "delete", mock.Anything).Return()
			}

			// Execute
			err := svc.TerminateSandbox(ctx, tt.sandboxID)

			// Assert
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify expectations
			k8sClient.AssertExpectations(t)
			metricsRecorder.AssertExpectations(t)
		})
	}
}
