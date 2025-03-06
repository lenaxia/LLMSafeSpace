package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

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
		logger: mockLogger,
		config: mockConfig,
		db:     db,
	}

	// Return the service, mock, and a cleanup function
	return service, mock, func() {
		db.Close()
	}
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
	apiKey := "test_api_key"
	expectedUserID := "user123"

	// Set up expectations for valid API key
	rows := sqlmock.NewRows([]string{"user_id"}).AddRow(expectedUserID)
	mock.ExpectQuery("SELECT user_id FROM api_keys WHERE key = \\$1 AND active = true").
		WithArgs(apiKey).
		WillReturnRows(rows)

	// Call the method
	userID, err := service.GetUserIDByAPIKey(apiKey)
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
	userID, err = service.GetUserIDByAPIKey(invalidKey)
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
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Test case: Database error
	mock.ExpectExec("INSERT INTO sandboxes").
		WithArgs("error_sandbox", userID, runtime, sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	// Call the method
	err = service.CreateSandboxMetadata(ctx, "error_sandbox", userID, runtime)
	if err == nil {
		t.Errorf("Expected error, got nil")
	}

	// Verify all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}
