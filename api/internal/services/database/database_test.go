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
	rows := sqlmock.NewRows([]string{"id", "username", "email", "password_hash", "created_at", "updated_at", "active", "role"}).
		AddRow(userID, username, email, "$2a$10$hash", createdAt, updatedAt, active, role)
	mock.ExpectQuery("SELECT id, username, email, password_hash, created_at, updated_at, active, role FROM users WHERE id = \\$1").
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
	mock.ExpectQuery("SELECT id, username, email, password_hash, created_at, updated_at, active, role FROM users WHERE id = \\$1").
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
	t.Run("success", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()
		mock.MatchExpectationsInOrder(false)

		ctx := context.Background()
		sandbox := &types.SandboxMetadata{
			ID:        "sandbox123",
			UserID:    "user456",
			Runtime:   "python:3.10",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Status:    "Running",
			Name:      "Test Sandbox",
			Labels:    map[string]string{"env": "test", "app": "demo"},
		}

		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO sandboxes").
			WithArgs(sandbox.ID, sandbox.UserID, sandbox.Runtime, sandbox.CreatedAt, sandbox.UpdatedAt, sandbox.Status, sandbox.Name, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO sandbox_labels").
			WithArgs(sandbox.ID, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO sandbox_labels").
			WithArgs(sandbox.ID, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		assert.NoError(t, service.CreateSandbox(ctx, sandbox))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db_error_rolls_back", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		errorSandbox := &types.SandboxMetadata{
			ID:      "error_sandbox",
			UserID:  "user456",
			Runtime: "python:3.10",
		}

		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO sandboxes").
			WithArgs(errorSandbox.ID, errorSandbox.UserID, errorSandbox.Runtime, sqlmock.AnyArg(), sqlmock.AnyArg(), errorSandbox.Status, errorSandbox.Name, sqlmock.AnyArg()).
			WillReturnError(sql.ErrConnDone)
		mock.ExpectRollback()

		err := service.CreateSandbox(ctx, errorSandbox)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create sandbox")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCreateWorkspace(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		ws := &types.WorkspaceMetadata{
			ID:          "ws-uuid-1",
			UserID:      "user-1",
			Name:        "My Workspace",
			Runtime:     "python:3.11",
			StorageSize: "10Gi",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		mock.ExpectExec("INSERT INTO workspaces").
			WithArgs(ws.ID, ws.UserID, ws.Name, ws.Runtime, ws.StorageSize, ws.CreatedAt, ws.UpdatedAt).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := service.CreateWorkspace(ctx, ws)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero_timestamps_auto_filled", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		ws := &types.WorkspaceMetadata{
			ID:          "ws-uuid-2",
			UserID:      "user-2",
			Name:        "Auto TS Workspace",
			Runtime:     "nodejs:18",
			StorageSize: "5Gi",
		}

		mock.ExpectExec("INSERT INTO workspaces").
			WithArgs(ws.ID, ws.UserID, ws.Name, ws.Runtime, ws.StorageSize, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := service.CreateWorkspace(ctx, ws)
		assert.NoError(t, err)
		assert.False(t, ws.CreatedAt.IsZero())
		assert.False(t, ws.UpdatedAt.IsZero())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db_error", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		ws := &types.WorkspaceMetadata{
			ID:     "ws-err",
			UserID: "user-err",
			Name:   "Error Workspace",
		}

		mock.ExpectExec("INSERT INTO workspaces").
			WithArgs(ws.ID, ws.UserID, ws.Name, ws.Runtime, ws.StorageSize, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnError(sql.ErrConnDone)

		err := service.CreateWorkspace(ctx, ws)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create workspace")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetWorkspace(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		now := time.Now()
		wsID := "ws-uuid-found"

		rows := sqlmock.NewRows([]string{"id", "user_id", "name", "runtime", "storage_size", "created_at", "updated_at"}).
			AddRow(wsID, "user-1", "My Workspace", "python:3.11", "10Gi", now, now)

		mock.ExpectQuery("SELECT id, user_id, name, runtime, storage_size, created_at, updated_at FROM workspaces WHERE id = \\$1").
			WithArgs(wsID).
			WillReturnRows(rows)

		ws, err := service.GetWorkspace(ctx, wsID)
		assert.NoError(t, err)
		assert.NotNil(t, ws)
		assert.Equal(t, wsID, ws.ID)
		assert.Equal(t, "user-1", ws.UserID)
		assert.Equal(t, "My Workspace", ws.Name)
		assert.Equal(t, "python:3.11", ws.Runtime)
		assert.Equal(t, "10Gi", ws.StorageSize)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not_found_returns_nil", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()

		mock.ExpectQuery("SELECT id, user_id, name, runtime, storage_size, created_at, updated_at FROM workspaces WHERE id = \\$1").
			WithArgs("nonexistent").
			WillReturnError(sql.ErrNoRows)

		ws, err := service.GetWorkspace(ctx, "nonexistent")
		assert.NoError(t, err)
		assert.Nil(t, ws)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db_error", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()

		mock.ExpectQuery("SELECT id, user_id, name, runtime, storage_size, created_at, updated_at FROM workspaces WHERE id = \\$1").
			WithArgs("ws-err").
			WillReturnError(sql.ErrConnDone)

		ws, err := service.GetWorkspace(ctx, "ws-err")
		assert.Error(t, err)
		assert.Nil(t, ws)
		assert.Contains(t, err.Error(), "failed to get workspace")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestListWorkspaces(t *testing.T) {
	t.Run("multiple_rows", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		userID := "user-list"
		limit := 10
		offset := 0
		now := time.Now()

		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM workspaces WHERE user_id = \\$1").
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

		wsRows := sqlmock.NewRows([]string{"id", "user_id", "name", "runtime", "storage_size", "created_at", "updated_at"}).
			AddRow("ws-1", userID, "Workspace One", "python:3.11", "5Gi", now, now).
			AddRow("ws-2", userID, "Workspace Two", "nodejs:18", "10Gi", now.Add(-time.Hour), now)

		mock.ExpectQuery("SELECT id, user_id, name, runtime, storage_size, created_at, updated_at FROM workspaces WHERE user_id = \\$1 ORDER BY created_at DESC LIMIT \\$2 OFFSET \\$3").
			WithArgs(userID, limit, offset).
			WillReturnRows(wsRows)

		wsList, pagination, err := service.ListWorkspaces(ctx, userID, limit, offset)
		assert.NoError(t, err)
		assert.Len(t, wsList, 2)
		assert.Equal(t, 2, pagination.Total)
		assert.Equal(t, "ws-1", wsList[0].ID)
		assert.Equal(t, "ws-2", wsList[1].ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty_result", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		userID := "user-empty"

		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM workspaces WHERE user_id = \\$1").
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		wsList, pagination, err := service.ListWorkspaces(ctx, userID, 10, 0)
		assert.NoError(t, err)
		assert.Len(t, wsList, 0)
		assert.Equal(t, 0, pagination.Total)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count_db_error", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()

		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM workspaces WHERE user_id = \\$1").
			WithArgs("user-err").
			WillReturnError(sql.ErrConnDone)

		_, _, err := service.ListWorkspaces(ctx, "user-err", 10, 0)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to count workspaces")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUpdateWorkspace(t *testing.T) {
	t.Run("name_updated", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		wsID := "ws-update"
		name := "New Name"

		mock.ExpectExec("UPDATE workspaces SET updated_at = NOW\\(\\), name = \\$1 WHERE id = \\$2").
			WithArgs(name, wsID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := service.UpdateWorkspace(ctx, wsID, types.WorkspaceUpdates{Name: &name})
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no_fields_is_noop", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		err := service.UpdateWorkspace(context.Background(), "ws-noop", types.WorkspaceUpdates{})
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db_error", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		wsID := "ws-err"
		name := "Error Update"

		mock.ExpectExec("UPDATE workspaces SET updated_at = NOW\\(\\), name = \\$1 WHERE id = \\$2").
			WithArgs(name, wsID).
			WillReturnError(sql.ErrConnDone)

		err := service.UpdateWorkspace(ctx, wsID, types.WorkspaceUpdates{Name: &name})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update workspace")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteWorkspace(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		wsID := "ws-delete"

		mock.ExpectExec("DELETE FROM workspaces WHERE id = \\$1").
			WithArgs(wsID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := service.DeleteWorkspace(ctx, wsID)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not_found_is_ok", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()

		mock.ExpectExec("DELETE FROM workspaces WHERE id = \\$1").
			WithArgs("nonexistent").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := service.DeleteWorkspace(ctx, "nonexistent")
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db_error", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()

		mock.ExpectExec("DELETE FROM workspaces WHERE id = \\$1").
			WithArgs("ws-err").
			WillReturnError(sql.ErrConnDone)

		err := service.DeleteWorkspace(ctx, "ws-err")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete workspace")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCreateUser(t *testing.T) {
	t.Run("success_with_explicit_timestamps", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		now := time.Now()
		user := &types.User{
			ID:           "user-abc",
			Username:     "alice",
			Email:        "alice@example.com",
			PasswordHash: "$2a$10$hash",
			CreatedAt:    now,
			UpdatedAt:    now,
			Active:       true,
			Role:         "user",
		}

		mock.ExpectExec("INSERT INTO users").
			WithArgs(user.ID, user.Username, user.Email, user.PasswordHash, user.CreatedAt, user.UpdatedAt, user.Active, user.Role).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := service.CreateUser(ctx, user)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success_zero_timestamps_auto_filled", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		user := &types.User{
			ID:       "user-xyz",
			Username: "bob",
			Email:    "bob@example.com",
			Active:   false,
			Role:     "admin",
			// CreatedAt and UpdatedAt intentionally zero
		}

		mock.ExpectExec("INSERT INTO users").
			WithArgs(user.ID, user.Username, user.Email, user.PasswordHash, sqlmock.AnyArg(), sqlmock.AnyArg(), user.Active, user.Role).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := service.CreateUser(ctx, user)
		assert.NoError(t, err)
		assert.False(t, user.CreatedAt.IsZero())
		assert.False(t, user.UpdatedAt.IsZero())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db_error", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		user := &types.User{
			ID:       "user-dup",
			Username: "dup",
			Email:    "dup@example.com",
		}

		mock.ExpectExec("INSERT INTO users").
			WithArgs(user.ID, user.Username, user.Email, user.PasswordHash, sqlmock.AnyArg(), sqlmock.AnyArg(), user.Active, user.Role).
			WillReturnError(sql.ErrConnDone)

		err := service.CreateUser(ctx, user)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create user")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteUser(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		userID := "user-to-delete"

		mock.ExpectExec("DELETE FROM users WHERE id = \\$1").
			WithArgs(userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := service.DeleteUser(ctx, userID)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("user_not_found_is_not_an_error", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()

		// DELETE affecting 0 rows is not an error in the current implementation
		mock.ExpectExec("DELETE FROM users WHERE id = \\$1").
			WithArgs("nonexistent").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := service.DeleteUser(ctx, "nonexistent")
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db_error", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()

		mock.ExpectExec("DELETE FROM users WHERE id = \\$1").
			WithArgs("user-err").
			WillReturnError(sql.ErrConnDone)

		err := service.DeleteUser(ctx, "user-err")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete user")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUpdateUser(t *testing.T) {
	t.Run("username_and_email", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		userID := "user-1"
		username := "alice"
		email := "alice@example.com"

		// Correct query: $1=username, $2=email, $3=userID
		mock.ExpectExec("UPDATE users SET updated_at = NOW\\(\\), username = \\$1, email = \\$2 WHERE id = \\$3").
			WithArgs(username, email, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := service.UpdateUser(ctx, userID, types.UserUpdates{
			Username: &username,
			Email:    &email,
		})
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("single_field_role", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		userID := "user-1"
		role := "admin"

		mock.ExpectExec("UPDATE users SET updated_at = NOW\\(\\), role = \\$1 WHERE id = \\$2").
			WithArgs(role, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := service.UpdateUser(ctx, userID, types.UserUpdates{Role: &role})
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("all_fields", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		userID := "user-1"
		username, email, role := "bob", "bob@example.com", "user"
		active := true

		// $1=username, $2=email, $3=active, $4=role, $5=userID
		mock.ExpectExec("UPDATE users SET updated_at = NOW\\(\\), username = \\$1, email = \\$2, active = \\$3, role = \\$4 WHERE id = \\$5").
			WithArgs(username, email, active, role, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := service.UpdateUser(ctx, userID, types.UserUpdates{
			Username: &username,
			Email:    &email,
			Active:   &active,
			Role:     &role,
		})
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no_fields_is_noop", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		// No SQL expected
		err := service.UpdateUser(context.Background(), "user-1", types.UserUpdates{})
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db_error", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		role := "admin"
		mock.ExpectExec("UPDATE users SET updated_at = NOW\\(\\), role = \\$1 WHERE id = \\$2").
			WithArgs(role, "user-1").
			WillReturnError(sql.ErrConnDone)

		err := service.UpdateUser(ctx, "user-1", types.UserUpdates{Role: &role})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update user")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
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
	sandboxRows := sqlmock.NewRows([]string{"id", "user_id", "runtime", "created_at", "updated_at", "status", "name", "workspace_id"}).
		AddRow("sandbox1", userID, "python:3.10", now, now, "Running", "Test Sandbox 1", nil).
		AddRow("sandbox2", userID, "nodejs:16", now.Add(-1*time.Hour), now, "Pending", "Test Sandbox 2", nil)

	mock.ExpectQuery("SELECT id, user_id, runtime, created_at, updated_at, status, name, workspace_id FROM sandboxes WHERE user_id = \\$1 ORDER BY created_at DESC LIMIT \\$2 OFFSET \\$3").
		WithArgs(userID, limit, offset).
		WillReturnRows(sandboxRows)

	// Set up expectations for labels query - using pq.Array for the sandbox IDs
	labelRows := sqlmock.NewRows([]string{"sandbox_id", "key", "value"}).
		AddRow("sandbox1", "env", "test").
		AddRow("sandbox1", "app", "demo").
		AddRow("sandbox2", "env", "prod")

	// Fix: Use a proper SQL query pattern that matches what the actual code will use
	// The issue is with how we're mocking the ANY($1) part of the query
	mock.ExpectQuery("SELECT sandbox_id, key, value FROM sandbox_labels WHERE sandbox_id IN \\('sandbox1','sandbox2'\\)").
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
	sandboxRows := sqlmock.NewRows([]string{"id", "user_id", "runtime", "created_at", "updated_at", "status", "name", "workspace_id"}).
		AddRow(sandboxID, userID, runtime, now, now, status, name, nil)

	mock.ExpectQuery("SELECT id, user_id, runtime, created_at, updated_at, status, name, workspace_id FROM sandboxes WHERE id = \\$1").
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
	mock.ExpectQuery("SELECT id, user_id, runtime, created_at, updated_at, status, name, workspace_id FROM sandboxes WHERE id = \\$1").
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
	t.Run("status_and_name_with_labels", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()
		mock.MatchExpectationsInOrder(false)

		ctx := context.Background()
		sandboxID := "sandbox123"
		status := "Completed"
		name := "Updated Sandbox"
		updates := types.SandboxUpdates{
			Status: &status,
			Name:   &name,
			Labels: map[string]string{"env": "prod", "app": "demo"},
		}

		mock.ExpectBegin()
		mock.ExpectExec("UPDATE sandboxes SET updated_at = NOW\\(\\)").
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sandboxID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("DELETE FROM sandbox_labels WHERE sandbox_id = \\$1").
			WithArgs(sandboxID).
			WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectExec("INSERT INTO sandbox_labels").
			WithArgs(sandboxID, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO sandbox_labels").
			WithArgs(sandboxID, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		assert.NoError(t, service.UpdateSandbox(ctx, sandboxID, updates))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("status_only", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		sandboxID := "sandbox123"
		status := "Running"
		updates := types.SandboxUpdates{Status: &status}

		mock.ExpectBegin()
		mock.ExpectExec("UPDATE sandboxes SET updated_at = NOW\\(\\), status = \\$1 WHERE id = \\$2").
			WithArgs("Running", sandboxID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		assert.NoError(t, service.UpdateSandbox(ctx, sandboxID, updates))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no_fields_set_is_noop", func(t *testing.T) {
		service, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctx := context.Background()
		// No SQL expected — empty update is a no-op
		assert.NoError(t, service.UpdateSandbox(ctx, "sandbox123", types.SandboxUpdates{}))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
