package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/app"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/lenaxia/llmsafespace/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestServer creates a test server with all middleware and routes
func setupTestServer(t *testing.T) *httptest.Server {
	// Load test configuration
	cfg, err := config.LoadConfig("../../../config/config.test.yaml")
	require.NoError(t, err)

	// Create logger
	log, err := logger.New(true, "debug", "console")
	require.NoError(t, err)

	// Create app
	app, err := app.New(cfg, log)
	require.NoError(t, err)

	// Start the app
	err = app.Start()
	require.NoError(t, err)

	// Create test server
	server := httptest.NewServer(app.Router)
	return server
}

// TestAuthenticationFlow tests the complete authentication flow
func TestAuthenticationFlow(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	server := setupTestServer(t)
	defer server.Close()

	client := server.Client()
	baseURL := server.URL

	// Step 1: Register a new user
	t.Log("Step 1: Registering a new user")
	registerPayload := map[string]interface{}{
		"username": fmt.Sprintf("testuser_%d", time.Now().Unix()),
		"email":    fmt.Sprintf("test_%d@example.com", time.Now().Unix()),
		"password": "Password123!",
	}
	registerBody, _ := json.Marshal(registerPayload)

	registerResp, err := client.Post(
		baseURL+"/api/v1/auth/register",
		"application/json",
		bytes.NewBuffer(registerBody),
	)
	require.NoError(t, err)
	defer registerResp.Body.Close()

	assert.Equal(t, http.StatusCreated, registerResp.StatusCode)

	var registerResult map[string]interface{}
	err = json.NewDecoder(registerResp.Body).Decode(&registerResult)
	require.NoError(t, err)
	assert.Contains(t, registerResult, "user")
	assert.Contains(t, registerResult, "token")

	// Extract token
	token := registerResult["token"].(string)
	userID := registerResult["user"].(map[string]interface{})["id"].(string)

	// Step 2: Login with the new user
	t.Log("Step 2: Logging in with the new user")
	loginPayload := map[string]interface{}{
		"username": registerPayload["username"],
		"password": registerPayload["password"],
	}
	loginBody, _ := json.Marshal(loginPayload)

	loginResp, err := client.Post(
		baseURL+"/api/v1/auth/login",
		"application/json",
		bytes.NewBuffer(loginBody),
	)
	require.NoError(t, err)
	defer loginResp.Body.Close()

	assert.Equal(t, http.StatusOK, loginResp.StatusCode)

	var loginResult map[string]interface{}
	err = json.NewDecoder(loginResp.Body).Decode(&loginResult)
	require.NoError(t, err)
	assert.Contains(t, loginResult, "token")

	// Step 3: Get user profile with token
	t.Log("Step 3: Getting user profile with token")
	req, _ := http.NewRequest("GET", baseURL+"/api/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	profileResp, err := client.Do(req)
	require.NoError(t, err)
	defer profileResp.Body.Close()

	assert.Equal(t, http.StatusOK, profileResp.StatusCode)

	var profileResult map[string]interface{}
	err = json.NewDecoder(profileResp.Body).Decode(&profileResult)
	require.NoError(t, err)
	assert.Equal(t, userID, profileResult["id"])
	assert.Equal(t, registerPayload["username"], profileResult["username"])
	assert.Equal(t, registerPayload["email"], profileResult["email"])

	// Step 4: Try to access protected resource without token
	t.Log("Step 4: Trying to access protected resource without token")
	noAuthReq, _ := http.NewRequest("GET", baseURL+"/api/v1/users/me", nil)
	noAuthResp, err := client.Do(noAuthReq)
	require.NoError(t, err)
	defer noAuthResp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, noAuthResp.StatusCode)

	// Step 5: Logout
	t.Log("Step 5: Logging out")
	logoutReq, _ := http.NewRequest("POST", baseURL+"/api/v1/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+token)

	logoutResp, err := client.Do(logoutReq)
	require.NoError(t, err)
	defer logoutResp.Body.Close()

	assert.Equal(t, http.StatusOK, logoutResp.StatusCode)

	// Step 6: Try to use token after logout
	t.Log("Step 6: Trying to use token after logout")
	afterLogoutReq, _ := http.NewRequest("GET", baseURL+"/api/v1/users/me", nil)
	afterLogoutReq.Header.Set("Authorization", "Bearer "+token)

	afterLogoutResp, err := client.Do(afterLogoutReq)
	require.NoError(t, err)
	defer afterLogoutResp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, afterLogoutResp.StatusCode)
}

// TestSandboxCreationFlow tests the complete sandbox creation and management flow
func TestSandboxCreationFlow(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	server := setupTestServer(t)
	defer server.Close()

	client := server.Client()
	baseURL := server.URL

	// Step 1: Login to get token
	t.Log("Step 1: Logging in to get token")
	loginPayload := map[string]interface{}{
		"username": "testuser",
		"password": "Password123!",
	}
	loginBody, _ := json.Marshal(loginPayload)

	loginResp, err := client.Post(
		baseURL+"/api/v1/auth/login",
		"application/json",
		bytes.NewBuffer(loginBody),
	)
	require.NoError(t, err)
	defer loginResp.Body.Close()

	var loginResult map[string]interface{}
	err = json.NewDecoder(loginResp.Body).Decode(&loginResult)
	require.NoError(t, err)
	token := loginResult["token"].(string)

	// Step 2: Create a sandbox
	t.Log("Step 2: Creating a sandbox")
	sandboxPayload := map[string]interface{}{
		"runtime":       "python:3.10",
		"securityLevel": "standard",
		"resources": map[string]interface{}{
			"cpu":    "500m",
			"memory": "512Mi",
		},
		"networkAccess": map[string]interface{}{
			"egress": []map[string]interface{}{
				{
					"domain": "pypi.org",
					"ports": []map[string]interface{}{
						{
							"port":     443,
							"protocol": "TCP",
						},
					},
				},
			},
			"ingress": false,
		},
	}
	sandboxBody, _ := json.Marshal(sandboxPayload)

	sandboxReq, _ := http.NewRequest(
		"POST",
		baseURL+"/api/v1/sandboxes",
		bytes.NewBuffer(sandboxBody),
	)
	sandboxReq.Header.Set("Authorization", "Bearer "+token)
	sandboxReq.Header.Set("Content-Type", "application/json")

	sandboxResp, err := client.Do(sandboxReq)
	require.NoError(t, err)
	defer sandboxResp.Body.Close()

	assert.Equal(t, http.StatusCreated, sandboxResp.StatusCode)

	var sandboxResult map[string]interface{}
	err = json.NewDecoder(sandboxResp.Body).Decode(&sandboxResult)
	require.NoError(t, err)
	assert.Contains(t, sandboxResult, "id")
	assert.Contains(t, sandboxResult, "status")

	sandboxID := sandboxResult["id"].(string)

	// Step 3: Execute code in the sandbox
	t.Log("Step 3: Executing code in the sandbox")
	executePayload := map[string]interface{}{
		"type":    "code",
		"content": "print('Hello, world!')",
		"timeout": 30,
	}
	executeBody, _ := json.Marshal(executePayload)

	executeReq, _ := http.NewRequest(
		"POST",
		fmt.Sprintf("%s/api/v1/sandboxes/%s/execute", baseURL, sandboxID),
		bytes.NewBuffer(executeBody),
	)
	executeReq.Header.Set("Authorization", "Bearer "+token)
	executeReq.Header.Set("Content-Type", "application/json")

	executeResp, err := client.Do(executeReq)
	require.NoError(t, err)
	defer executeResp.Body.Close()

	assert.Equal(t, http.StatusOK, executeResp.StatusCode)

	var executeResult map[string]interface{}
	err = json.NewDecoder(executeResp.Body).Decode(&executeResult)
	require.NoError(t, err)
	assert.Contains(t, executeResult, "stdout")
	assert.Contains(t, executeResult["stdout"], "Hello, world!")

	// Step 4: Upload a file to the sandbox
	t.Log("Step 4: Uploading a file to the sandbox")
	fileContent := []byte("print('This is a test file')")
	fileReq, _ := http.NewRequest(
		"PUT",
		fmt.Sprintf("%s/api/v1/sandboxes/%s/files/test.py", baseURL, sandboxID),
		bytes.NewBuffer(fileContent),
	)
	fileReq.Header.Set("Authorization", "Bearer "+token)
	fileReq.Header.Set("Content-Type", "application/octet-stream")

	fileResp, err := client.Do(fileReq)
	require.NoError(t, err)
	defer fileResp.Body.Close()

	assert.Equal(t, http.StatusOK, fileResp.StatusCode)

	// Step 5: List files in the sandbox
	t.Log("Step 5: Listing files in the sandbox")
	listReq, _ := http.NewRequest(
		"GET",
		fmt.Sprintf("%s/api/v1/sandboxes/%s/files", baseURL, sandboxID),
		nil,
	)
	listReq.Header.Set("Authorization", "Bearer "+token)

	listResp, err := client.Do(listReq)
	require.NoError(t, err)
	defer listResp.Body.Close()

	assert.Equal(t, http.StatusOK, listResp.StatusCode)

	var listResult map[string]interface{}
	err = json.NewDecoder(listResp.Body).Decode(&listResult)
	require.NoError(t, err)
	assert.Contains(t, listResult, "files")

	files := listResult["files"].([]interface{})
	foundFile := false
	for _, file := range files {
		fileMap := file.(map[string]interface{})
		if fileMap["name"] == "test.py" {
			foundFile = true
			break
		}
	}
	assert.True(t, foundFile, "Uploaded file not found in file listing")

	// Step 6: Execute the uploaded file
	t.Log("Step 6: Executing the uploaded file")
	executeFilePayload := map[string]interface{}{
		"type":    "command",
		"content": "python test.py",
		"timeout": 30,
	}
	executeFileBody, _ := json.Marshal(executeFilePayload)

	executeFileReq, _ := http.NewRequest(
		"POST",
		fmt.Sprintf("%s/api/v1/sandboxes/%s/execute", baseURL, sandboxID),
		bytes.NewBuffer(executeFileBody),
	)
	executeFileReq.Header.Set("Authorization", "Bearer "+token)
	executeFileReq.Header.Set("Content-Type", "application/json")

	executeFileResp, err := client.Do(executeFileReq)
	require.NoError(t, err)
	defer executeFileResp.Body.Close()

	assert.Equal(t, http.StatusOK, executeFileResp.StatusCode)

	var executeFileResult map[string]interface{}
	err = json.NewDecoder(executeFileResp.Body).Decode(&executeFileResult)
	require.NoError(t, err)
	assert.Contains(t, executeFileResult, "stdout")
	assert.Contains(t, executeFileResult["stdout"], "This is a test file")

	// Step 7: Terminate the sandbox
	t.Log("Step 7: Terminating the sandbox")
	terminateReq, _ := http.NewRequest(
		"DELETE",
		fmt.Sprintf("%s/api/v1/sandboxes/%s", baseURL, sandboxID),
		nil,
	)
	terminateReq.Header.Set("Authorization", "Bearer "+token)

	terminateResp, err := client.Do(terminateReq)
	require.NoError(t, err)
	defer terminateResp.Body.Close()

	assert.Equal(t, http.StatusOK, terminateResp.StatusCode)

	// Step 8: Verify sandbox is terminated
	t.Log("Step 8: Verifying sandbox is terminated")
	getReq, _ := http.NewRequest(
		"GET",
		fmt.Sprintf("%s/api/v1/sandboxes/%s", baseURL, sandboxID),
		nil,
	)
	getReq.Header.Set("Authorization", "Bearer "+token)

	getResp, err := client.Do(getReq)
	require.NoError(t, err)
	defer getResp.Body.Close()

	// Should either return 404 or status "Terminated"
	if getResp.StatusCode == http.StatusOK {
		var getSandboxResult map[string]interface{}
		err = json.NewDecoder(getResp.Body).Decode(&getSandboxResult)
		require.NoError(t, err)
		assert.Equal(t, "Terminated", getSandboxResult["status"].(map[string]interface{})["phase"])
	} else {
		assert.Equal(t, http.StatusNotFound, getResp.StatusCode)
	}
}

// TestRateLimitingFlow tests the rate limiting functionality
func TestRateLimitingFlow(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Create a mock logger
	log, _ := logger.New(true, "debug", "console")

	// Configure rate limiting for testing
	rateLimitConfig := middleware.RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  3,                // Very low limit for testing
		DefaultWindow: 10 * time.Second, // Short window for testing
		Strategy:      "token_bucket",
		BurstSize:     3,
	}

	// Create a test endpoint with rate limiting
	router.Use(func(c *gin.Context) {
		// Set API key in context for rate limiting
		c.Set("apiKey", "test-api-key")
		c.Next()
	})

	// Add rate limiting middleware
	mockRateLimiter := new(mocks.MockRateLimiterService)
	router.Use(middleware.RateLimitMiddleware(mockRateLimiter, log, rateLimitConfig))

	// Test endpoint
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	// Create test server
	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()
	baseURL := server.URL

	// Configure mock rate limiter
	// First 3 requests allowed
	mockRateLimiter.On("Allow", mock.Anything, mock.Anything, mock.Anything).Return(true).Times(3)
	// Subsequent requests denied
	mockRateLimiter.On("Allow", mock.Anything, mock.Anything, mock.Anything).Return(false)

	// Step 1: Make allowed requests
	t.Log("Step 1: Making allowed requests")
	for i := 0; i < 3; i++ {
		resp, err := client.Get(baseURL + "/test")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "3", resp.Header.Get("X-RateLimit-Limit"))
		remaining, _ := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))
		assert.Equal(t, 2-i, remaining)
	}

	// Step 2: Make request that exceeds rate limit
	t.Log("Step 2: Making request that exceeds rate limit")
	resp, err := client.Get(baseURL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, "3", resp.Header.Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", resp.Header.Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, resp.Header.Get("X-RateLimit-Reset"))

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "rate_limited", result["error"].(map[string]interface{})["code"])

	mockRateLimiter.AssertExpectations(t)
}

// TestErrorHandlingFlow tests the error handling functionality
func TestErrorHandlingFlow(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Create a mock logger
	log, _ := logger.New(true, "debug", "console")

	// Add error handling middleware
	router.Use(middleware.ErrorHandlerMiddleware(log))

	// Add validation middleware
	router.Use(middleware.ValidationMiddleware(log))

	// Test endpoints
	router.POST("/validation", func(c *gin.Context) {
		// Set validation model
		type TestModel struct {
			Name  string `json:"name" validate:"required,min=3"`
			Email string `json:"email" validate:"required,email"`
			Age   int    `json:"age" validate:"required,gte=18"`
		}
		c.Set("validationModel", &TestModel{})
		c.Next()
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "validation passed"})
	})

	router.GET("/not-found", func(c *gin.Context) {
		middleware.HandleAPIError(c, errors.NewNotFoundError("Resource", "123", nil))
	})

	router.GET("/forbidden", func(c *gin.Context) {
		middleware.HandleAPIError(c, errors.NewForbiddenError("Access denied", nil))
	})

	router.GET("/internal-error", func(c *gin.Context) {
		middleware.HandleAPIError(c, errors.NewInternalError("Something went wrong", fmt.Errorf("database error")))
	})

	router.GET("/panic", func(c *gin.Context) {
		panic("test panic")
	})

	// Add recovery middleware after routes to test panic recovery
	router.Use(middleware.RecoveryMiddleware(log))

	// Create test server
	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()
	baseURL := server.URL

	// Step 1: Test validation error
	t.Log("Step 1: Testing validation error")
	validationPayload := map[string]interface{}{
		"name":  "Jo", // Too short
		"email": "not-an-email",
		"age":   16, // Too young
	}
	validationBody, _ := json.Marshal(validationPayload)

	validationResp, err := client.Post(
		baseURL+"/validation",
		"application/json",
		bytes.NewBuffer(validationBody),
	)
	require.NoError(t, err)
	defer validationResp.Body.Close()

	assert.Equal(t, http.StatusUnprocessableEntity, validationResp.StatusCode)

	var validationResult map[string]interface{}
	err = json.NewDecoder(validationResp.Body).Decode(&validationResult)
	require.NoError(t, err)
	assert.Equal(t, "validation_error", validationResult["error"].(map[string]interface{})["code"])
	
	details := validationResult["error"].(map[string]interface{})["details"].(map[string]interface{})
	errors := details["errors"].(map[string]interface{})
	assert.Contains(t, errors, "name")
	assert.Contains(t, errors, "email")
	assert.Contains(t, errors, "age")

	// Step 2: Test not found error
	t.Log("Step 2: Testing not found error")
	notFoundResp, err := client.Get(baseURL + "/not-found")
	require.NoError(t, err)
	defer notFoundResp.Body.Close()

	assert.Equal(t, http.StatusNotFound, notFoundResp.StatusCode)

	var notFoundResult map[string]interface{}
	err = json.NewDecoder(notFoundResp.Body).Decode(&notFoundResult)
	require.NoError(t, err)
	assert.Equal(t, "not_found", notFoundResult["error"].(map[string]interface{})["code"])

	// Step 3: Test forbidden error
	t.Log("Step 3: Testing forbidden error")
	forbiddenResp, err := client.Get(baseURL + "/forbidden")
	require.NoError(t, err)
	defer forbiddenResp.Body.Close()

	assert.Equal(t, http.StatusForbidden, forbiddenResp.StatusCode)

	var forbiddenResult map[string]interface{}
	err = json.NewDecoder(forbiddenResp.Body).Decode(&forbiddenResult)
	require.NoError(t, err)
	assert.Equal(t, "forbidden", forbiddenResult["error"].(map[string]interface{})["code"])

	// Step 4: Test internal error
	t.Log("Step 4: Testing internal error")
	internalResp, err := client.Get(baseURL + "/internal-error")
	require.NoError(t, err)
	defer internalResp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, internalResp.StatusCode)

	var internalResult map[string]interface{}
	err = json.NewDecoder(internalResp.Body).Decode(&internalResult)
	require.NoError(t, err)
	assert.Equal(t, "internal_error", internalResult["error"].(map[string]interface{})["code"])

	// Step 5: Test panic recovery
	t.Log("Step 5: Testing panic recovery")
	panicResp, err := client.Get(baseURL + "/panic")
	require.NoError(t, err)
	defer panicResp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, panicResp.StatusCode)

	var panicResult map[string]interface{}
	err = json.NewDecoder(panicResp.Body).Decode(&panicResult)
	require.NoError(t, err)
	assert.Equal(t, "internal_error", panicResult["error"].(map[string]interface{})["code"])
}

// TestSecurityHeadersFlow tests the security headers functionality
func TestSecurityHeadersFlow(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Create a mock logger
	log, _ := logger.New(true, "debug", "console")

	// Add security middleware
	securityConfig := middleware.SecurityConfig{
		ContentSecurityPolicy: "default-src 'self'",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		PermissionsPolicy:     "camera=(), microphone=()",
		RequireHTTPS:          false, // Disable for testing
		Development:           true,
	}
	router.Use(middleware.SecurityMiddleware(log, securityConfig))

	// Test endpoint
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	// Create test server
	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()
	baseURL := server.URL

	// Test security headers
	t.Log("Testing security headers")
	resp, err := client.Get(baseURL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Check security headers
	assert.Equal(t, "default-src 'self'", resp.Header.Get("Content-Security-Policy"))
	assert.Equal(t, "strict-origin-when-cross-origin", resp.Header.Get("Referrer-Policy"))
	assert.Equal(t, "camera=(), microphone=()", resp.Header.Get("Permissions-Policy"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "none", resp.Header.Get("X-Permitted-Cross-Domain-Policies"))
	assert.Equal(t, "noopen", resp.Header.Get("X-Download-Options"))
}
