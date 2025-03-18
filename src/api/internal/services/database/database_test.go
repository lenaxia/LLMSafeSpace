package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
)

// Helper function to create a test config
func createTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Database.Host = "localhost"
	cfg.Database.Port = 5432
	cfg.Database.User = "test"
	cfg.Database.Password = "test"
	cfg.Database.Database = "test"
	cfg.Database.SSLMode = "disable"
	cfg.Database.MaxOpenConns = 10
	cfg.Database.MaxIdleConns = 5
	cfg.Database.ConnMaxLifetime = time.Hour
	return cfg
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

func TestGetUserByAPIKey(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: Valid API key
	ctx := context.Background()
	apiKey := "test_api_key"
	expectedUserID := "user123"
	expectedUsername := "testuser"
	expectedEmail := "test@example.com"
	expectedCreatedAt := time.Now()
	expectedUpdatedAt := time.Now()
	expectedActive := true
	expectedRole := "user"

	// Set up expectations for valid API key
	rows := sqlmock.NewRows([]string{"id", "username", "email", "created_at", "updated_at", "active", "role"}).
		AddRow(expectedUserID, expectedUsername, expectedEmail, expectedCreatedAt, expectedUpdatedAt, expectedActive, expectedRole)
	
	mock.ExpectQuery("SELECT u.id, u.username, u.email, u.created_at, u.updated_at, u.active, u.role FROM users u JOIN api_keys k").
		WithArgs(apiKey).
		WillReturnRows(rows)

	// Call the method
	user, err := service.GetUserByAPIKey(ctx, apiKey)
	assert.NoError(t, err)
	assert.NotNil(t, user)
	assert.Equal(t, expectedUserID, user.ID)
	assert.Equal(t, expectedUsername, user.Username)
	assert.Equal(t, expectedEmail, user.Email)
	assert.Equal(t, expectedActive, user.Active)
	assert.Equal(t, expectedRole, user.Role)

	// Test case: Invalid API key
	invalidKey := "invalid_key"
	mock.ExpectQuery("SELECT u.id, u.username, u.email, u.created_at, u.updated_at, u.active, u.role FROM users u JOIN api_keys k").
		WithArgs(invalidKey).
		WillReturnError(sql.ErrNoRows)

	// Call the method
	user, err = service.GetUserByAPIKey(ctx, invalidKey)
	assert.NoError(t, err)
	assert.Nil(t, user)

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
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

func TestGetUser(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: User exists
	ctx := context.Background()
	userID := "user123"
	username := "testuser"
	email := "test@example.com"
	createdAt := time.Now()
	updatedAt := time.Now()
	active := true
	role := "user"

	// Set up expectations
	rows := sqlmock.NewRows([]string{"id", "username", "email", "created_at", "updated_at", "active", "role"}).
		AddRow(userID, username, email, createdAt, updatedAt, active, role)
	mock.ExpectQuery("SELECT id, username, email, created_at, updated_at, active, role FROM users WHERE id = \\$1").
		WithArgs(userID).
		WillReturnRows(rows)

	// Call the method
	user, err := service.GetUser(ctx, userID)
	assert.NoError(t, err)
	assert.NotNil(t, user)
	assert.Equal(t, userID, user.ID)
	assert.Equal(t, username, user.Username)
	assert.Equal(t, email, user.Email)
	assert.Equal(t, active, user.Active)
	assert.Equal(t, role, user.Role)

	// Test case: User not found
	mock.ExpectQuery("SELECT id, username, email, created_at, updated_at, active, role FROM users WHERE id = \\$1").
		WithArgs("nonexistent").
		WillReturnError(sql.ErrNoRows)

	// Call the method
	user, err = service.GetUser(ctx, "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, user)

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSandbox(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: Create sandbox
	ctx := context.Background()
	sandbox := &types.SandboxMetadata{
		ID:        "sandbox123",
		UserID:    "user456",
		Runtime:   "python:3.10",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Status:    "Running",
		Name:      "Test Sandbox",
		Labels: map[string]string{
			"env": "test",
			"app": "demo",
		},
	}

	// Set up expectations for transaction
	mock.ExpectBegin()
	
	// Expect insert into sandboxes table
	mock.ExpectExec("INSERT INTO sandboxes").
		WithArgs(sandbox.ID, sandbox.UserID, sandbox.Runtime, sandbox.CreatedAt, sandbox.UpdatedAt, sandbox.Status, sandbox.Name).
		WillReturnResult(sqlmock.NewResult(1, 1))
	
	// Expect inserts for labels
	mock.ExpectExec("INSERT INTO sandbox_labels").
		WithArgs(sandbox.ID, "env", "test").
		WillReturnResult(sqlmock.NewResult(1, 1))
	
	mock.ExpectExec("INSERT INTO sandbox_labels").
		WithArgs(sandbox.ID, "app", "demo").
		WillReturnResult(sqlmock.NewResult(1, 1))
	
	// Expect commit
	mock.ExpectCommit()

	// Call the method
	err := service.CreateSandbox(ctx, sandbox)
	assert.NoError(t, err)

	// Test case: Database error
	errorSandbox := &types.SandboxMetadata{
		ID:      "error_sandbox",
		UserID:  "user456",
		Runtime: "python:3.10",
	}
	
	// Set up expectations for transaction with error
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO sandboxes").
		WithArgs(errorSandbox.ID, errorSandbox.UserID, errorSandbox.Runtime, sqlmock.AnyArg(), sqlmock.AnyArg(), errorSandbox.Status, errorSandbox.Name).
		WillReturnError(sql.ErrConnDone)
	
	// Expect rollback
	mock.ExpectRollback()

	// Call the method
	err = service.CreateSandbox(ctx, errorSandbox)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create sandbox")

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
	
	// Set up expectations for count query
	countRows := sqlmock.NewRows([]string{"count"}).AddRow(2)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM sandboxes WHERE user_id = \\$1").
		WithArgs(userID).
		WillReturnRows(countRows)
	
	// Set up expectations for sandboxes query
	now := time.Now()
	sandboxRows := sqlmock.NewRows([]string{"id", "user_id", "runtime", "created_at", "updated_at", "status", "name"}).
		AddRow("sandbox1", userID, "python:3.10", now, now, "Running", "Test Sandbox 1").
		AddRow("sandbox2", userID, "nodejs:16", now.Add(-1*time.Hour), now, "Pending", "Test Sandbox 2")
	
	mock.ExpectQuery("SELECT id, user_id, runtime, created_at, updated_at, status, name FROM sandboxes WHERE user_id = \\$1 ORDER BY created_at DESC LIMIT \\$2 OFFSET \\$3").
		WithArgs(userID, limit, offset).
		WillReturnRows(sandboxRows)
	
	// Set up expectations for labels query
	labelRows := sqlmock.NewRows([]string{"sandbox_id", "key", "value"}).
		AddRow("sandbox1", "env", "test").
		AddRow("sandbox1", "app", "demo").
		AddRow("sandbox2", "env", "prod")
	
	mock.ExpectQuery("SELECT sandbox_id, key, value FROM sandbox_labels WHERE sandbox_id = ANY").
		WithArgs([]string{"sandbox1", "sandbox2"}).
		WillReturnRows(labelRows)

	// Call the method
	sandboxes, pagination, err := service.ListSandboxes(ctx, userID, limit, offset)
	assert.NoError(t, err)
	assert.NotNil(t, sandboxes)
	assert.Len(t, sandboxes, 2)
	
	// Check first sandbox
	assert.Equal(t, "sandbox1", sandboxes[0].ID)
	assert.Equal(t, userID, sandboxes[0].UserID)
	assert.Equal(t, "python:3.10", sandboxes[0].Runtime)
	assert.Equal(t, "Running", sandboxes[0].Status)
	assert.Equal(t, "Test Sandbox 1", sandboxes[0].Name)
	assert.Equal(t, "test", sandboxes[0].Labels["env"])
	assert.Equal(t, "demo", sandboxes[0].Labels["app"])
	
	// Check second sandbox
	assert.Equal(t, "sandbox2", sandboxes[1].ID)
	assert.Equal(t, "nodejs:16", sandboxes[1].Runtime)
	assert.Equal(t, "Pending", sandboxes[1].Status)
	assert.Equal(t, "Test Sandbox 2", sandboxes[1].Name)
	assert.Equal(t, "prod", sandboxes[1].Labels["env"])
	
	// Check pagination
	assert.NotNil(t, pagination)
	assert.Equal(t, 2, pagination.Total)
	assert.Equal(t, 0, pagination.Start)
	assert.Equal(t, 2, pagination.End)
	assert.Equal(t, 10, pagination.Limit)
	assert.Equal(t, 0, pagination.Offset)

	// Test case: Empty result
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM sandboxes WHERE user_id = \\$1").
		WithArgs("empty_user").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	// Call the method
	sandboxes, pagination, err = service.ListSandboxes(ctx, "empty_user", limit, offset)
	assert.NoError(t, err)
	assert.NotNil(t, sandboxes)
	assert.Len(t, sandboxes, 0)
	assert.NotNil(t, pagination)
	assert.Equal(t, 0, pagination.Total)

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSandbox(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: Get sandbox
	ctx := context.Background()
	sandboxID := "sandbox123"
	userID := "user456"
	runtime := "python:3.10"
	now := time.Now()
	status := "Running"
	name := "Test Sandbox"

	// Set up expectations for sandbox query
	sandboxRows := sqlmock.NewRows([]string{"id", "user_id", "runtime", "created_at", "updated_at", "status", "name"}).
		AddRow(sandboxID, userID, runtime, now, now, status, name)
	
	mock.ExpectQuery("SELECT id, user_id, runtime, created_at, updated_at, status, name FROM sandboxes WHERE id = \\$1").
		WithArgs(sandboxID).
		WillReturnRows(sandboxRows)
	
	// Set up expectations for labels query
	labelRows := sqlmock.NewRows([]string{"key", "value"}).
		AddRow("env", "test").
		AddRow("app", "demo")
	
	mock.ExpectQuery("SELECT key, value FROM sandbox_labels WHERE sandbox_id = \\$1").
		WithArgs(sandboxID).
		WillReturnRows(labelRows)

	// Call the method
	sandbox, err := service.GetSandbox(ctx, sandboxID)
	assert.NoError(t, err)
	assert.NotNil(t, sandbox)
	assert.Equal(t, sandboxID, sandbox.ID)
	assert.Equal(t, userID, sandbox.UserID)
	assert.Equal(t, runtime, sandbox.Runtime)
	assert.Equal(t, status, sandbox.Status)
	assert.Equal(t, name, sandbox.Name)
	assert.Equal(t, "test", sandbox.Labels["env"])
	assert.Equal(t, "demo", sandbox.Labels["app"])

	// Test case: Sandbox not found
	mock.ExpectQuery("SELECT id, user_id, runtime, created_at, updated_at, status, name FROM sandboxes WHERE id = \\$1").
		WithArgs("nonexistent").
		WillReturnError(sql.ErrNoRows)

	// Call the method
	sandbox, err = service.GetSandbox(ctx, "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, sandbox)

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSandbox(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: Delete sandbox
	ctx := context.Background()
	sandboxID := "sandbox123"

	// Set up expectations for transaction
	mock.ExpectBegin()
	
	// Expect delete from labels table
	mock.ExpectExec("DELETE FROM sandbox_labels WHERE sandbox_id = \\$1").
		WithArgs(sandboxID).
		WillReturnResult(sqlmock.NewResult(0, 2))
	
	// Expect delete from sandboxes table
	mock.ExpectExec("DELETE FROM sandboxes WHERE id = \\$1").
		WithArgs(sandboxID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	
	// Expect commit
	mock.ExpectCommit()

	// Call the method
	err := service.DeleteSandbox(ctx, sandboxID)
	assert.NoError(t, err)

	// Test case: Database error
	// Set up expectations for transaction with error
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM sandbox_labels WHERE sandbox_id = \\$1").
		WithArgs("error_sandbox").
		WillReturnError(sql.ErrConnDone)
	
	// Expect rollback
	mock.ExpectRollback()

	// Call the method
	err = service.DeleteSandbox(ctx, "error_sandbox")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete sandbox labels")

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSandbox(t *testing.T) {
	service, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// Test case: Update sandbox
	ctx := context.Background()
	sandboxID := "sandbox123"
	updates := map[string]interface{}{
		"status": "Completed",
		"name":   "Updated Sandbox",
		"labels": map[string]string{
			"env": "prod",
			"app": "demo",
		},
	}

	// Set up expectations for transaction
	mock.ExpectBegin()
	
	// Expect update to sandboxes table
	mock.ExpectExec("UPDATE sandboxes SET updated_at = NOW\\(\\), status = \\$1, name = \\$2 WHERE id = \\$3").
		WithArgs("Completed", "Updated Sandbox", sandboxID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	
	// Expect delete from labels table
	mock.ExpectExec("DELETE FROM sandbox_labels WHERE sandbox_id = \\$1").
		WithArgs(sandboxID).
		WillReturnResult(sqlmock.NewResult(0, 2))
	
	// Expect inserts for new labels
	mock.ExpectExec("INSERT INTO sandbox_labels").
		WithArgs(sandboxID, "env", "prod").
		WillReturnResult(sqlmock.NewResult(1, 1))
	
	mock.ExpectExec("INSERT INTO sandbox_labels").
		WithArgs(sandboxID, "app", "demo").
		WillReturnResult(sqlmock.NewResult(1, 1))
	
	// Expect commit
	mock.ExpectCommit()

	// Call the method
	err := service.UpdateSandbox(ctx, sandboxID, updates)
	assert.NoError(t, err)

	// Test case: Update only status
	statusUpdate := map[string]interface{}{
		"status": "Running",
	}

	// Set up expectations for transaction
	mock.ExpectBegin()
	
	// Expect update to sandboxes table
	mock.ExpectExec("UPDATE sandboxes SET updated_at = NOW\\(\\), status = \\$1 WHERE id = \\$2").
		WithArgs("Running", sandboxID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	
	// Expect commit
	mock.ExpectCommit()

	// Call the method
	err = service.UpdateSandbox(ctx, sandboxID, statusUpdate)
	assert.NoError(t, err)

	// Verify all expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}
