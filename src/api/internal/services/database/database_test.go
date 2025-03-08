package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/stretchr/testify/assert"
)

// Helper function to create a test config
func createTestConfig() *config.Config {
	return &config.Config{
		Database: config.Database{
			Host:            "localhost",
			Port:            5432,
			User:            "test",
			Password:        "test",
			Database:        "test",
			SSLMode:         "disable",
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: time.Hour,
		},
	}
}

func setupMockDB(t *testing.T) (*Service, sqlmock.Sqlmock, func()) {
	// Create a mock database connection
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}

	// Create a mock logger
	mockLogger, err := logger.New(true, "debug", "console")
	if err != nil {
		t.Fatalf("Failed to create mock logger: %v", err)
	}

	// Create a mock config
	mockConfig := &config.Config{}
	mockConfig.Database.MaxOpenConns = 10
	mockConfig.Database.MaxIdleConns = 5
	mockConfig.Database.ConnMaxLifetime = 5 * time.Minute

	// Create the database service with the mock DB
	service := &Service{
		Logger: mockLogger,
		Config: mockConfig,
		DB:     db,
	}

	// Return the service, mock, and a cleanup function
	return service, mock, func() {
		db.Close()
	}
}

func TestNew(t *testing.T) {
	// Create test dependencies
	log, _ := logger.New(true, "debug", "console")
	cfg := createTestConfig()

	// Mock the database
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}
	defer db.Close()

	// Create service with mocked DB
	service := &Service{
		Logger: log,
		Config: cfg,
		DB:     db,
	}

	// Test service creation
	assert.NotNil(t, service)
	assert.Equal(t, log, service.Logger)
	assert.Equal(t, cfg, service.Config)
	assert.Equal(t, db, service.DB)
}

func TestPing(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Set up expectations
	mock.ExpectPing()

	// Call the Ping method
	ctx := context.Background()
	err := service.Ping(ctx)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Verify all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestGetUserIDByAPIKey(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: Valid API key
	ctx := context.Background()
	apiKey := "test_api_key"
	expectedUserID := "user123"

	// Set up expectations for valid API key
	rows := sqlmock.NewRows([]string{"user_id"}).AddRow(expectedUserID)
	mock.ExpectQuery("SELECT user_id FROM api_keys WHERE key = \\$1 AND active = true").
		WithArgs(apiKey).
		WillReturnRows(rows)

	// Call the method
	userID, err := service.GetUserIDByAPIKey(ctx, apiKey)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if userID != expectedUserID {
		t.Errorf("Expected user ID %s, got %s", expectedUserID, userID)
	}

	// Test case: Invalid API key
	invalidKey := "invalid_key"
	mock.ExpectQuery("SELECT user_id FROM api_keys WHERE key = \\$1 AND active = true").
		WithArgs(invalidKey).
		WillReturnError(sql.ErrNoRows)

	// Call the method
	userID, err = service.GetUserIDByAPIKey(ctx, invalidKey)
	if err != nil {
		t.Errorf("Expected no error for invalid key, got %v", err)
	}
	if userID != "" {
		t.Errorf("Expected empty user ID for invalid key, got %s", userID)
	}

	// Verify all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestCheckResourceOwnership(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: User owns the resource
	userID := "user123"
	resourceID := "resource456"
	resourceType := "sandbox"

	// Set up expectations for owned resource
	rows := sqlmock.NewRows([]string{"count"}).AddRow(1)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM sandboxes WHERE id = \\$1 AND user_id = \\$2").
		WithArgs(resourceID, userID).
		WillReturnRows(rows)

	// Call the method
	owned, err := service.CheckResourceOwnership(userID, resourceType, resourceID)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !owned {
		t.Errorf("Expected resource to be owned by user")
	}

	// Test case: User does not own the resource
	otherUserID := "otheruser"
	rows = sqlmock.NewRows([]string{"count"}).AddRow(0)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM sandboxes WHERE id = \\$1 AND user_id = \\$2").
		WithArgs(resourceID, otherUserID).
		WillReturnRows(rows)

	// Call the method
	owned, err = service.CheckResourceOwnership(otherUserID, resourceType, resourceID)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if owned {
		t.Errorf("Expected resource not to be owned by user")
	}

	// Test case: Unsupported resource type
	_, err = service.CheckResourceOwnership(userID, "unsupported", resourceID)
	if err == nil {
		t.Errorf("Expected error for unsupported resource type, got nil")
	}

	// Verify all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestCheckPermission(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: User has permission
	userID := "user123"
	resourceType := "sandbox"
	resourceID := "resource456"
	action := "read"

	// Set up expectations for permission check
	rows := sqlmock.NewRows([]string{"count"}).AddRow(1)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM permissions").
		WithArgs(userID, resourceType, resourceID, action).
		WillReturnRows(rows)

	// Call the method
	hasPermission, err := service.CheckPermission(userID, resourceType, resourceID, action)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !hasPermission {
		t.Errorf("Expected user to have permission")
	}

	// Test case: User does not have permission
	rows = sqlmock.NewRows([]string{"count"}).AddRow(0)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM permissions").
		WithArgs(userID, resourceType, resourceID, "write").
		WillReturnRows(rows)

	// Call the method
	hasPermission, err = service.CheckPermission(userID, resourceType, resourceID, "write")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if hasPermission {
		t.Errorf("Expected user not to have permission")
	}

	// Verify all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestGetUserByID(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: User exists
	ctx := context.Background()
	userID := "user123"
	username := "testuser"
	email := "test@example.com"
	createdAt := time.Now()

	// Set up expectations
	rows := sqlmock.NewRows([]string{"id", "username", "email", "created_at"}).
		AddRow(userID, username, email, createdAt)
	mock.ExpectQuery("SELECT id, username, email, created_at FROM users WHERE id = \\$1").
		WithArgs(userID).
		WillReturnRows(rows)

	// Call the method
	user, err := service.GetUserByID(ctx, userID)
	assert.NoError(t, err)
	assert.NotNil(t, user)
	assert.Equal(t, userID, user["id"])
	assert.Equal(t, username, user["username"])
	assert.Equal(t, email, user["email"])

	// Test case: User not found
	mock.ExpectQuery("SELECT id, username, email, created_at FROM users WHERE id = \\$1").
		WithArgs("nonexistent").
		WillReturnError(sql.ErrNoRows)

	// Call the method
	user, err = service.GetUserByID(ctx, "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, user)

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSandboxMetadata(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: Create sandbox metadata
	ctx := context.Background()
	sandboxID := "sandbox123"
	userID := "user456"
	runtime := "python:3.10"

	// Set up expectations
	mock.ExpectExec("INSERT INTO sandboxes").
		WithArgs(sandboxID, userID, runtime, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Call the method
	err := service.CreateSandboxMetadata(ctx, sandboxID, userID, runtime)
	assert.NoError(t, err)

	// Test case: Database error
	mock.ExpectExec("INSERT INTO sandboxes").
		WithArgs("error_sandbox", userID, runtime, sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	// Call the method
	err = service.CreateSandboxMetadata(ctx, "error_sandbox", userID, runtime)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create sandbox metadata")

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSandboxMetadata(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: Delete sandbox metadata
	ctx := context.Background()
	sandboxID := "sandbox123"

	// Set up expectations
	mock.ExpectExec("DELETE FROM sandboxes WHERE id = \\$1").
		WithArgs(sandboxID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Call the method
	err := service.DeleteSandboxMetadata(ctx, sandboxID)
	assert.NoError(t, err)

	// Test case: Database error
	mock.ExpectExec("DELETE FROM sandboxes WHERE id = \\$1").
		WithArgs("error_sandbox").
		WillReturnError(sql.ErrConnDone)

	// Call the method
	err = service.DeleteSandboxMetadata(ctx, "error_sandbox")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete sandbox metadata")

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSandboxMetadata(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: Get sandbox metadata
	ctx := context.Background()
	sandboxID := "sandbox123"
	userID := "user456"
	runtime := "python:3.10"
	createdAt := time.Now()

	// Set up expectations
	rows := sqlmock.NewRows([]string{"id", "user_id", "runtime", "created_at"}).
		AddRow(sandboxID, userID, runtime, createdAt)
	mock.ExpectQuery("SELECT id, user_id, runtime, created_at FROM sandboxes WHERE id = \\$1").
		WithArgs(sandboxID).
		WillReturnRows(rows)

	// Call the method
	metadata, err := service.GetSandboxMetadata(ctx, sandboxID)
	assert.NoError(t, err)
	assert.NotNil(t, metadata)
	assert.Equal(t, sandboxID, metadata["id"])
	assert.Equal(t, userID, metadata["user_id"])
	assert.Equal(t, runtime, metadata["runtime"])

	// Test case: Sandbox not found
	mock.ExpectQuery("SELECT id, user_id, runtime, created_at FROM sandboxes WHERE id = \\$1").
		WithArgs("nonexistent").
		WillReturnError(sql.ErrNoRows)

	// Call the method
	metadata, err = service.GetSandboxMetadata(ctx, "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, metadata)

	// Test case: Database error
	mock.ExpectQuery("SELECT id, user_id, runtime, created_at FROM sandboxes WHERE id = \\$1").
		WithArgs("error_sandbox").
		WillReturnError(sql.ErrConnDone)

	// Call the method
	metadata, err = service.GetSandboxMetadata(ctx, "error_sandbox")
	assert.Error(t, err)
	assert.Nil(t, metadata)
	assert.Contains(t, err.Error(), "failed to get sandbox metadata")

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListSandboxes(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: List sandboxes
	ctx := context.Background()
	userID := "user123"
	limit := 10
	offset := 0
	createdAt := time.Now()

	// Set up expectations
	rows := sqlmock.NewRows([]string{"id", "runtime", "created_at"}).
		AddRow("sandbox1", "python:3.10", createdAt).
		AddRow("sandbox2", "nodejs:16", createdAt)
	mock.ExpectQuery("SELECT id, runtime, created_at FROM sandboxes WHERE user_id = \\$1 ORDER BY created_at DESC LIMIT \\$2 OFFSET \\$3").
		WithArgs(userID, limit, offset).
		WillReturnRows(rows)

	// Call the method
	sandboxes, err := service.ListSandboxes(ctx, userID, limit, offset)
	assert.NoError(t, err)
	assert.Len(t, sandboxes, 2)
	assert.Equal(t, "sandbox1", sandboxes[0]["id"])
	assert.Equal(t, "python:3.10", sandboxes[0]["runtime"])
	assert.Equal(t, "sandbox2", sandboxes[1]["id"])
	assert.Equal(t, "nodejs:16", sandboxes[1]["runtime"])

	// Test case: Database error
	mock.ExpectQuery("SELECT id, runtime, created_at FROM sandboxes WHERE user_id = \\$1 ORDER BY created_at DESC LIMIT \\$2 OFFSET \\$3").
		WithArgs("error_user", limit, offset).
		WillReturnError(sql.ErrConnDone)

	// Call the method
	sandboxes, err = service.ListSandboxes(ctx, "error_user", limit, offset)
	assert.Error(t, err)
	assert.Nil(t, sandboxes)
	assert.Contains(t, err.Error(), "failed to list sandboxes")

	// Test case: Row scan error
	rows = sqlmock.NewRows([]string{"id", "runtime"}).  // Missing created_at column
		AddRow("sandbox1", "python:3.10")
	mock.ExpectQuery("SELECT id, runtime, created_at FROM sandboxes WHERE user_id = \\$1 ORDER BY created_at DESC LIMIT \\$2 OFFSET \\$3").
		WithArgs("scan_error", limit, offset).
		WillReturnRows(rows)

	// Call the method
	sandboxes, err = service.ListSandboxes(ctx, "scan_error", limit, offset)
	assert.Error(t, err)
	assert.Nil(t, sandboxes)
	assert.Contains(t, err.Error(), "failed to scan sandbox row")

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}
