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
	resourceType := "workspace"

	// Set up expectations for owned resource
	rows := sqlmock.NewRows([]string{"count"}).AddRow(1)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM workspaces WHERE id = \\$1 AND user_id = \\$2").
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
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM workspaces WHERE id = \\$1 AND user_id = \\$2").
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
	resourceType := "workspace"
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




