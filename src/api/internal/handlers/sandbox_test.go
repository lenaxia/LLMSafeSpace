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
	"github.com/lenaxia/llmsafespace/api/internal/services/execution"
	"github.com/lenaxia/llmsafespace/api/internal/services/file"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// MockSandboxService implementation
type MockSandboxService struct {
	mock.Mock
	sandbox.Service
}

func (m *MockSandboxService) CreateSandbox(ctx context.Context, req sandbox.CreateSandboxRequest) (*llmsafespacev1.Sandbox, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.Sandbox), args.Error(1)
}

func (m *MockSandboxService) GetSandbox(ctx context.Context, sandboxID string) (*llmsafespacev1.Sandbox, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.Sandbox), args.Error(1)
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

func (m *MockSandboxService) GetSandboxStatus(ctx context.Context, sandboxID string) (*llmsafespacev1.SandboxStatus, error) {
	args := m.Called(ctx, sandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llmsafespacev1.SandboxStatus), args.Error(1)
}

func (m *MockSandboxService) Execute(ctx context.Context, req sandbox.ExecuteRequest) (*execution.Result, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*execution.Result), args.Error(1)
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

func (m *MockSandboxService) InstallPackages(ctx context.Context, req sandbox.InstallPackagesRequest) (*execution.Result, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*execution.Result), args.Error(1)
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

// MockAuthService implementation
type MockAuthService struct {
	mock.Mock
}

func (m *MockAuthService) GetUserFromContext(c *gin.Context) (string, error) {
	args := m.Called(c)
	return args.String(0), args.Error(1)
}

func (m *MockAuthService) CheckResourceAccess(c *gin.Context, resourceType, resourceID, action string) bool {
	args := m.Called(c, resourceType, resourceID, action)
	return args.Bool(0)
}

func setupSandboxHandler(t *testing.T) (*SandboxHandler, *MockSandboxService, *MockAuthService, *gin.Engine) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	mockSandboxService := new(MockSandboxService)
	mockAuthService := new(MockAuthService)

	// Create handler
	handler := &SandboxHandler{
		logger:     log,
		sandboxSvc: mockSandboxService,
		authSvc:    mockAuthService,
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockSandboxService.On("CreateSandbox", mock.Anything, mock.MatchedBy(func(req sandbox.CreateSandboxRequest) bool {
		return req.Runtime == "python:3.10" && req.UserID == "user123"
	})).Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-12345",
		},
		Status: llmsafespacev1.SandboxStatus{
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("", errors.New("unauthorized")).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Invalid request body
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes", bytes.NewBuffer([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-12345", "read").Return(true).Once()
	mockSandboxService.On("GetSandbox", mock.Anything, "sb-12345").Return(&llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sb-12345",
		},
		Spec: llmsafespacev1.SandboxSpec{
			Runtime: "python:3.10",
		},
		Status: llmsafespacev1.SandboxStatus{
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("", errors.New("unauthorized")).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Access denied
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-12345", "read").Return(false).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Test case: Sandbox not found
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "nonexistent", "read").Return(true).Once()
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("", errors.New("unauthorized")).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-12345", "delete").Return(true).Once()
	mockSandboxService.On("TerminateSandbox", mock.Anything, "sb-12345").Return(nil).Once()

	// Create request
	req := httptest.NewRequest("DELETE", "/api/v1/sandboxes/sb-12345", nil)
	w := httptest.NewRecorder()

	// Serve the request
	router.ServeHTTP(w, req)

	// Check response
	assert.Equal(t, http.StatusOK, w.Code)

	// Test case: Authentication error
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("", errors.New("unauthorized")).Once()

	req = httptest.NewRequest("DELETE", "/api/v1/sandboxes/sb-12345", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Access denied
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-12345", "delete").Return(false).Once()

	req = httptest.NewRequest("DELETE", "/api/v1/sandboxes/sb-12345", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-error", "delete").Return(true).Once()
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-12345", "read").Return(true).Once()
	mockSandboxService.On("GetSandboxStatus", mock.Anything, "sb-12345").Return(&llmsafespacev1.SandboxStatus{
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("", errors.New("unauthorized")).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345/status", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Access denied
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-12345", "read").Return(false).Once()

	req = httptest.NewRequest("GET", "/api/v1/sandboxes/sb-12345/status", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-error", "read").Return(true).Once()
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-12345", "execute").Return(true).Once()
	mockSandboxService.On("Execute", mock.Anything, mock.MatchedBy(func(req sandbox.ExecuteRequest) bool {
		return req.Type == "code" && req.Content == "print('Hello, World!')" && req.SandboxID == "sb-12345"
	})).Return(&execution.Result{
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
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("", errors.New("unauthorized")).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes/sb-12345/execute", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test case: Access denied
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-12345", "execute").Return(false).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes/sb-12345/execute", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Test case: Invalid request body
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-12345", "execute").Return(true).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes/sb-12345/execute", bytes.NewBuffer([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Test case: Service error
	mockAuthService.On("GetUserFromContext", mock.Anything).Return("user123", nil).Once()
	mockAuthService.On("CheckResourceAccess", mock.Anything, "sandbox", "sb-error", "execute").Return(true).Once()
	mockSandboxService.On("Execute", mock.Anything, mock.Anything).Return(nil, errors.New("service error")).Once()

	req = httptest.NewRequest("POST", "/api/v1/sandboxes/sb-error/execute", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	mockSandboxService.AssertExpectations(t)
	mockAuthService.AssertExpectations(t)
}
