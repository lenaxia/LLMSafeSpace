package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/execution"
	"github.com/lenaxia/llmsafespace/api/internal/services/file"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/api/internal/types"
)

// MockSandboxService implementation
type MockSandboxService struct {
        mock.Mock
}

// Ensure mock implements the interface
var _ SandboxService = (*MockSandboxService)(nil)

func (m *MockSandboxService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockSandboxService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockSandboxService) CreateSandbox(ctx context.Context, req sandbox.CreateSandboxRequest) (*types.Sandbox, error) {
        args := m.Called(ctx, req)
        if args.Get(0) == nil {
                return nil, args.Error(1)
        }
        return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *MockSandboxService) GetSandbox(ctx context.Context, sandboxID string) (*types.Sandbox, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.Sandbox), args.Error(1)
}

func (m *MockSandboxService) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (m *MockSandboxService) TerminateSandbox(ctx context.Context, sandboxID string) error {
	args := m.Called(ctx, sandboxID)
	return args.Error(0)
}

func (m *MockSandboxService) GetSandboxStatus(ctx context.Context, sandboxID string) (*types.SandboxStatus, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxStatus), args.Error(1)
}

func (m *MockSandboxService) Execute(ctx context.Context, req sandbox.ExecuteRequest) (*types.ExecutionResult, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

func (m *MockSandboxService) ListFiles(ctx context.Context, sandboxID, path string) ([]file.FileInfo, error) {
	args := m.Called(ctx, sandboxID, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]file.FileInfo), args.Error(1)
}

func (m *MockSandboxService) DownloadFile(ctx context.Context, sandboxID, path string) ([]byte, error) {
	args := m.Called(ctx, sandboxID, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockSandboxService) UploadFile(ctx context.Context, sandboxID, path string, content []byte) (*file.FileInfo, error) {
	args := m.Called(ctx, sandboxID, path, content)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*file.FileInfo), args.Error(1)
}

func (m *MockSandboxService) DeleteFile(ctx context.Context, sandboxID, path string) error {
	args := m.Called(ctx, sandboxID, path)
	return args.Error(0)
}

func (m *MockSandboxService) InstallPackages(ctx context.Context, req sandbox.InstallPackagesRequest) (*types.ExecutionResult, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.ExecutionResult), args.Error(1)
}

func (m *MockSandboxService) CreateSession(userID, sandboxID string, conn *websocket.Conn) (*sandbox.Session, error) {
	args := m.Called(userID, sandboxID, conn)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sandbox.Session), args.Error(1)
}

func (m *MockSandboxService) CloseSession(sessionID string) {
	m.Called(sessionID)
}

func (m *MockSandboxService) HandleSession(session *sandbox.Session) {
	m.Called(session)
}

func (m *MockSandboxService) GetMetrics() map[string]interface{} {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(map[string]interface{})
}

// MockAuthService implementation
type MockAuthService struct {
	mock.Mock
}

// Ensure mock implements the interface
var _ AuthService = (*MockAuthService)(nil)

func (m *MockAuthService) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAuthService) Stop() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAuthService) GetUserID(c *gin.Context) string {
	args := m.Called(c)
	return args.String(0)
}

func (m *MockAuthService) CheckResourceAccess(userID, resourceType, resourceID, action string) bool {
	args := m.Called(userID, resourceType, resourceID, action)
	return args.Bool(0)
}

func (m *MockAuthService) GenerateToken(userID string) (string, error) {
	args := m.Called(userID)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) ValidateToken(token string) (string, error) {
	args := m.Called(token)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) AuthenticateAPIKey(ctx context.Context, apiKey string) (string, error) {
	args := m.Called(ctx, apiKey)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) AuthMiddleware() gin.HandlerFunc {
	args := m.Called()
	return args.Get(0).(gin.HandlerFunc)
}

func setupSandboxHandler(t *testing.T) (*SandboxHandler, *MockSandboxService, *MockAuthService, *gin.Engine) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockSandboxService := new(MockSandboxService)
	mockAuthService := new(MockAuthService)

	// Create handler
	handler := &SandboxHandler{
		logger:     log,
		sandboxSvc: mockSandboxService, // Interface-compatible
		authSvc:    mockAuthService,    // Interface-compatible
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}

	// Create a test router
	gin.SetMode(gin.TestMode)
	router := gin.New()
	
	// Register routes
	v1 := router.Group("/api/v1")
	handler.RegisterRoutes(v1)

	return handler, mockSandboxService, mockAuthService, router
}

func TestCreateSandbox(t *testing.T) {
	_, mockSandboxService, mockAuthService, router := setupSandboxHandler(t)

	// Test case: Successful creation
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockSandboxService.On("CreateSandbox", mock.Anything, mock.MatchedBy(func(req sandbox.CreateSandboxRequest) bool {
		return req.Runtime == "python:3.10" && req.UserID == "user123"
	})).Return(&types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-12345",
		},
		Status: types.SandboxStatus{
			Phase: "Creating",
		},
	}, nil).Once()

	// Create request body
	reqBody := map[string]interface{}{
		"runtime":       "python:3.10",
		"securityLevel": "standard",
		"timeout":       300,
	}
	jsonBody, _ := json.Marshal(reqBody)

	// Create request
	req := httptest.NewRequest("POST", "/api/v1/sandboxes", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Serve the request
	router.ServeHTTP(w, req)

	// Check response
	assert.Equal(t, http.StatusCreated, w.Code)
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "sb-12345", response["id"])
	assert.Equal(t, "Creating", response["status"])

	// Test case: Authentication error
	mockAuthService.On("GetUserID", mock.Anything).Return("").Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Invalid request body
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes", bytes.NewBuffer([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockSandboxService.On("CreateSandbox", mock.Anything, mock.Anything).Return(nil, errors.New("service error")).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	mockSandboxService.AssertExpectations(t)
	mockAuthService.AssertExpectations(t)
}

func TestGetSandbox(t *testing.T) {
	_, mockSandboxService, mockAuthService, router := setupSandboxHandler(t)

	// Test case: Successful get
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-12345", "read").Return(true).Once()
	mockSandboxService.On("GetSandbox", mock.Anything, "sb-12345").Return(&types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-12345",
		},
		Spec: types.SandboxSpec{
			Runtime: "python:3.10",
		},
		Status: types.SandboxStatus{
			Phase: "Running",
		},
	}, nil).Once()

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345", nil)
	w := httptest.NewRecorder()

	// Serve the request
	router.ServeHTTP(w, req)

	// Check response
	assert.Equal(t, http.StatusOK, w.Code)
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "sb-12345", response["id"])
	assert.Equal(t, "Running", response["status"])

	// Test case: Authentication error
	mockAuthService.On("GetUserID", mock.Anything).Return("").Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Access denied
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-12345", "read").Return(false).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Test case: Sandbox not found
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "nonexistent", "read").Return(true).Once()
	mockSandboxService.On("GetSandbox", mock.Anything, "nonexistent").Return(nil, errors.New("sandbox not found")).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/nonexistent", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	mockSandboxService.AssertExpectations(t)
	mockAuthService.AssertExpectations(t)
}

func TestListSandboxes(t *testing.T) {
	_, mockSandboxService, mockAuthService, router := setupSandboxHandler(t)

	// Test case: Successful list
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockSandboxService.On("ListSandboxes", mock.Anything, "user123", 10, 0).Return([]map[string]interface{}{
		{
			"id":      "sb-12345",
			"runtime": "python:3.10",
			"status":  "Running",
		},
		{
			"id":      "sb-67890",
			"runtime": "nodejs:16",
			"status":  "Running",
		},
	}, nil).Once()

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/sandboxes?limit=10&offset=0", nil)
	w := httptest.NewRecorder()

	// Serve the request
	router.ServeHTTP(w, req)

	// Check response
	assert.Equal(t, http.StatusOK, w.Code)
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	
	sandboxes, ok := response["sandboxes"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, sandboxes, 2)

	// Test case: Authentication error
	mockAuthService.On("GetUserID", mock.Anything).Return("").Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockSandboxService.On("ListSandboxes", mock.Anything, "user123", 10, 0).Return(nil, errors.New("service error")).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes?limit=10&offset=0", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	mockSandboxService.AssertExpectations(t)
	mockAuthService.AssertExpectations(t)
}

func TestTerminateSandbox(t *testing.T) {
	_, mockSandboxService, mockAuthService, router := setupSandboxHandler(t)

	// Test case: Successful termination
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-12345", "delete").Return(true).Once()
	mockSandboxService.On("TerminateSandbox", mock.Anything, "sb-12345").Return(nil).Once()

	// Create request
	req := httptest.NewRequest("DELETE", "/api/v1/sandboxes/sb-12345", nil)
	w := httptest.NewRecorder()

	// Serve the request
	router.ServeHTTP(w, req)

	// Check response
	assert.Equal(t, http.StatusOK, w.Code)

	// Test case: Authentication error
	mockAuthService.On("GetUserID", mock.Anything).Return("").Once()

	req = httptest.NewRequest("DELETE", "/api/v1/sandboxes/sb-12345", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Access denied
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-12345", "delete").Return(false).Once()

	req = httptest.NewRequest("DELETE", "/api/v1/sandboxes/sb-12345", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-error", "delete").Return(true).Once()
	mockSandboxService.On("TerminateSandbox", mock.Anything, "sb-error").Return(errors.New("service error")).Once()

	req = httptest.NewRequest("DELETE", "/api/v1/sandboxes/sb-error", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	mockSandboxService.AssertExpectations(t)
	mockAuthService.AssertExpectations(t)
}

func TestGetSandboxStatus(t *testing.T) {
	_, mockSandboxService, mockAuthService, router := setupSandboxHandler(t)

	// Test case: Successful get status
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-12345", "read").Return(true).Once()
	mockSandboxService.On("GetSandboxStatus", mock.Anything, "sb-12345").Return(&types.SandboxStatus{
		Phase:    "Running",
		Endpoint: "sb-12345.default.svc.cluster.local",
	}, nil).Once()

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345/status", nil)
	w := httptest.NewRecorder()

	// Serve the request
	router.ServeHTTP(w, req)

	// Check response
	assert.Equal(t, http.StatusOK, w.Code)
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "Running", response["phase"])
	assert.Equal(t, "sb-12345.default.svc.cluster.local", response["endpoint"])

	// Test case: Authentication error
	mockAuthService.On("GetUserID", mock.Anything).Return("").Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345/status", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Access denied
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-12345", "read").Return(false).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345/status", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-error", "read").Return(true).Once()
	mockSandboxService.On("GetSandboxStatus", mock.Anything, "sb-error").Return(nil, errors.New("service error")).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/sb-error/status", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	mockSandboxService.AssertExpectations(t)
	mockAuthService.AssertExpectations(t)
}

func TestExecute(t *testing.T) {
	_, mockSandboxService, mockAuthService, router := setupSandboxHandler(t)

	// Test case: Successful execution
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-12345", "execute").Return(true).Once()
	mockSandboxService.On("Execute", mock.Anything, mock.MatchedBy(func(req sandbox.ExecuteRequest) bool {
		return req.Type == "code" && req.Content == "print('Hello, World!')" && req.SandboxID == "sb-12345"
	})).Return(&types.ExecutionResult{
		ExitCode: 0,
		Stdout:   "Hello, World!\n",
		Stderr:   "",
	}, nil).Once()

	// Create request body
	reqBody := map[string]interface{}{
		"type":    "code",
		"content": "print('Hello, World!')",
		"timeout": 30,
	}
	jsonBody, _ := json.Marshal(reqBody)

	// Create request
	req := httptest.NewRequest("POST", "/api/v1/sandboxes/sb-12345/execute", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Serve the request
	router.ServeHTTP(w, req)

	// Check response
	assert.Equal(t, http.StatusOK, w.Code)
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, float64(0), response["exitCode"])
	assert.Equal(t, "Hello, World!\n", response["stdout"])

	// Test case: Authentication error
	mockAuthService.On("GetUserID", mock.Anything).Return("").Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes/sb-12345/execute", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Access denied
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-12345", "execute").Return(false).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes/sb-12345/execute", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Test case: Invalid request body
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-12345", "execute").Return(true).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes/sb-12345/execute", bytes.NewBuffer([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserID", mock.Anything).Return("user123").Once()
	mockAuthService.On("CheckResourceAccess", "user123", "sandbox", "sb-error", "execute").Return(true).Once()
	mockSandboxService.On("Execute", mock.Anything, mock.Anything).Return(nil, errors.New("service error")).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes/sb-error/execute", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	mockSandboxService.AssertExpectations(t)
	mockAuthService.AssertExpectations(t)
}
