package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// Service handles database operations
type Service struct {
	Logger *logger.Logger
	Config *config.Config
	DB     *sql.DB
}

// New creates a new database service
func New(cfg *config.Config, log *logger.Logger) (*Service, error) {
	// Create connection string
	connString := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.Database,
		cfg.Database.SSLMode,
	)

	// Connect to database
	db, err := sql.Open("pgx", connString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Service{
		Logger: log,
		Config: cfg,
		DB:     db,
	}, nil
}

// Start starts the database service
func (s *Service) Start() error {
	s.Logger.Info("Database service started")
	return nil
}

// Stop stops the database service
func (s *Service) Stop() error {
	s.Logger.Info("Stopping database service")
	return s.DB.Close()
}

// Ensure Service implements the DatabaseService interface
var _ interfaces.DatabaseService = (*Service)(nil) // Compile-time interface check

// Ping checks the database connection
func (s *Service) Ping(ctx context.Context) error {
	return s.DB.PingContext(ctx)
}

// GetUser gets a user by ID
func (s *Service) GetUser(ctx context.Context, userID string) (*types.User, error) {
    query := `
        SELECT id, username, email, created_at, updated_at, active, role
        FROM users 
        WHERE id = $1
    `
    
    var user types.User
    
    err := s.DB.QueryRowContext(ctx, query, userID).Scan(
        &user.ID,
        &user.Username,
        &user.Email,
        &user.CreatedAt,
        &user.UpdatedAt,
        &user.Active,
        &user.Role,
    )
    
    if err != nil {
        if err == sql.ErrNoRows {
            return nil, nil
        }
        return nil, fmt.Errorf("failed to get user by ID: %w", err)
    }
    
    return &user, nil
}

// CreateUser creates a new user
func (s *Service) CreateUser(ctx context.Context, user *types.User) error {
    query := `
        INSERT INTO users (id, username, email, created_at, updated_at, active, role)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
    
    now := time.Now()
    if user.CreatedAt.IsZero() {
        user.CreatedAt = now
    }
    if user.UpdatedAt.IsZero() {
        user.UpdatedAt = now
    }
    
    _, err := s.DB.ExecContext(ctx, query,
        user.ID,
        user.Username,
        user.Email,
        user.CreatedAt,
        user.UpdatedAt,
        user.Active,
        user.Role,
    )
    
    if err != nil {
        return fmt.Errorf("failed to create user: %w", err)
    }
    
    return nil
}

// UpdateUser updates a user
func (s *Service) UpdateUser(ctx context.Context, userID string, updates map[string]interface{}) error {
    // Build dynamic query based on updates
    query := "UPDATE users SET updated_at = NOW()"
    args := []interface{}{}
    i := 1
    
    for key, value := range updates {
        // Only allow specific fields to be updated
        switch key {
        case "username", "email", "active", "role":
            query += fmt.Sprintf(", %s = $%d", key, i+1)
            args = append(args, value)
            i++
        }
    }
    
    query += fmt.Sprintf(" WHERE id = $%d", i+1)
    args = append(args, userID)
    
    // If no valid updates, just return
    if i == 1 {
        return nil
    }
    
    _, err := s.DB.ExecContext(ctx, query, args...)
    if err != nil {
        return fmt.Errorf("failed to update user: %w", err)
    }
    
    return nil
}

// DeleteUser deletes a user
func (s *Service) DeleteUser(ctx context.Context, userID string) error {
    query := `DELETE FROM users WHERE id = $1`
    
    _, err := s.DB.ExecContext(ctx, query, userID)
    if err != nil {
        return fmt.Errorf("failed to delete user: %w", err)
    }
    
    return nil
}

// GetUserByAPIKey gets the user associated with an API key
func (s *Service) GetUserByAPIKey(ctx context.Context, apiKey string) (*types.User, error) {
    query := `
        SELECT u.id, u.username, u.email, u.created_at, u.updated_at, u.active, u.role
        FROM users u
        JOIN api_keys k ON u.id = k.user_id
        WHERE k.key = $1 AND k.active = true
    `
    
    var user types.User
    
    err := s.DB.QueryRowContext(ctx, query, apiKey).Scan(
        &user.ID,
        &user.Username,
        &user.Email,
        &user.CreatedAt,
        &user.UpdatedAt,
        &user.Active,
        &user.Role,
    )
    
    if err != nil {
        if err == sql.ErrNoRows {
            return nil, nil
        }
        return nil, fmt.Errorf("failed to get user by API key: %w", err)
    }
    
    return &user, nil
}

// GetSandbox gets a sandbox by ID
func (s *Service) GetSandbox(ctx context.Context, sandboxID string) (*types.SandboxMetadata, error) {
    query := `
        SELECT id, user_id, runtime, created_at, updated_at, status, name
        FROM sandboxes
        WHERE id = $1
    `
    
    var sandbox types.SandboxMetadata
    var name sql.NullString
    
    err := s.DB.QueryRowContext(ctx, query, sandboxID).Scan(
        &sandbox.ID,
        &sandbox.UserID,
        &sandbox.Runtime,
        &sandbox.CreatedAt,
        &sandbox.UpdatedAt,
        &sandbox.Status,
        &name,
    )
    
    if err != nil {
        if err == sql.ErrNoRows {
            return nil, nil
        }
        return nil, fmt.Errorf("failed to get sandbox: %w", err)
    }
    
    if name.Valid {
        sandbox.Name = name.String
    }
    
    // Get labels if any
    labelsQuery := `
        SELECT key, value
        FROM sandbox_labels
        WHERE sandbox_id = $1
    `
    
    rows, err := s.DB.QueryContext(ctx, labelsQuery, sandboxID)
    if err != nil && err != sql.ErrNoRows {
        return nil, fmt.Errorf("failed to get sandbox labels: %w", err)
    }
    
    if err != sql.ErrNoRows {
        defer rows.Close()
        
        sandbox.Labels = make(map[string]string)
        for rows.Next() {
            var key, value string
            if err := rows.Scan(&key, &value); err != nil {
                return nil, fmt.Errorf("failed to scan sandbox label: %w", err)
            }
            sandbox.Labels[key] = value
        }
        
        if err := rows.Err(); err != nil {
            return nil, fmt.Errorf("error iterating sandbox labels: %w", err)
        }
    }
    
    return &sandbox, nil
}

// CreateSandbox creates a new sandbox
func (s *Service) CreateSandbox(ctx context.Context, sandbox *types.SandboxMetadata) error {
    tx, err := s.DB.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %w", err)
    }
    
    defer func() {
        if err != nil {
            tx.Rollback()
        }
    }()
    
    query := `
        INSERT INTO sandboxes (id, user_id, runtime, created_at, updated_at, status, name)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
    
    now := time.Now()
    if sandbox.CreatedAt.IsZero() {
        sandbox.CreatedAt = now
    }
    if sandbox.UpdatedAt.IsZero() {
        sandbox.UpdatedAt = now
    }
    
    _, err = tx.ExecContext(ctx, query,
        sandbox.ID,
        sandbox.UserID,
        sandbox.Runtime,
        sandbox.CreatedAt,
        sandbox.UpdatedAt,
        sandbox.Status,
        sandbox.Name,
    )
    
    if err != nil {
        return fmt.Errorf("failed to create sandbox: %w", err)
    }
    
    // Insert labels if any
    if len(sandbox.Labels) > 0 {
        labelQuery := `
            INSERT INTO sandbox_labels (sandbox_id, key, value)
            VALUES ($1, $2, $3)
        `
        
        for key, value := range sandbox.Labels {
            _, err = tx.ExecContext(ctx, labelQuery, sandbox.ID, key, value)
            if err != nil {
                return fmt.Errorf("failed to insert sandbox label: %w", err)
            }
        }
    }
    
    if err = tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %w", err)
    }
    
    return nil
}

// UpdateSandbox updates a sandbox
func (s *Service) UpdateSandbox(ctx context.Context, sandboxID string, updates map[string]interface{}) error {
    tx, err := s.DB.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %w", err)
    }
    
    defer func() {
        if err != nil {
            tx.Rollback()
        }
    }()
    
    // Build dynamic query based on updates
    query := "UPDATE sandboxes SET updated_at = NOW()"
    args := []interface{}{}
    i := 0
    
    for key, value := range updates {
        // Only allow specific fields to be updated
        switch key {
        case "status", "name":
            query += fmt.Sprintf(", %s = $%d", key, i+1)
            args = append(args, value)
            i++
        case "labels":
            // Handle labels separately
            continue
        }
    }
    
    query += fmt.Sprintf(" WHERE id = $%d", i+1)
    args = append(args, sandboxID)
    
    // If there are updates to the main table
    if i > 0 {
        _, err = tx.ExecContext(ctx, query, args...)
        if err != nil {
            return fmt.Errorf("failed to update sandbox: %w", err)
        }
    }
    
    // Handle labels update if present
    if labels, ok := updates["labels"].(map[string]string); ok {
        // Delete existing labels
        _, err = tx.ExecContext(ctx, "DELETE FROM sandbox_labels WHERE sandbox_id = $1", sandboxID)
        if err != nil {
            return fmt.Errorf("failed to delete existing labels: %w", err)
        }
        
        // Insert new labels
        labelQuery := `
            INSERT INTO sandbox_labels (sandbox_id, key, value)
            VALUES ($1, $2, $3)
        `
        
        for key, value := range labels {
            _, err = tx.ExecContext(ctx, labelQuery, sandboxID, key, value)
            if err != nil {
                return fmt.Errorf("failed to insert sandbox label: %w", err)
            }
        }
    }
    
    if err = tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %w", err)
    }
    
    return nil
}

// DeleteSandbox deletes a sandbox
func (s *Service) DeleteSandbox(ctx context.Context, sandboxID string) error {
    tx, err := s.DB.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %w", err)
    }
    
    defer func() {
        if err != nil {
            tx.Rollback()
        }
    }()
    
    // Delete labels first (foreign key constraint)
    _, err = tx.ExecContext(ctx, "DELETE FROM sandbox_labels WHERE sandbox_id = $1", sandboxID)
    if err != nil {
        return fmt.Errorf("failed to delete sandbox labels: %w", err)
    }
    
    // Delete the sandbox
    _, err = tx.ExecContext(ctx, "DELETE FROM sandboxes WHERE id = $1", sandboxID)
    if err != nil {
        return fmt.Errorf("failed to delete sandbox: %w", err)
    }
    
    if err = tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %w", err)
    }
    
    return nil
}

// ListSandboxes lists sandboxes for a user
func (s *Service) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]*types.SandboxMetadata, *types.PaginationMetadata, error) {
    // Get total count first
    var total int
    countErr := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sandboxes WHERE user_id = $1", userID).Scan(&total)
    if countErr != nil {
        return nil, nil, fmt.Errorf("failed to count sandboxes: %w", countErr)
    }
    
    // Create pagination metadata
    pagination := &types.PaginationMetadata{
        Total:  total,
        Start:  offset,
        End:    offset + limit,
        Limit:  limit,
        Offset: offset,
    }
    
    if pagination.End > total {
        pagination.End = total
    }
    
    // If no results, return empty slice
    if total == 0 {
        return []*types.SandboxMetadata{}, pagination, nil
    }
    
    // Query sandboxes
    query := `
        SELECT id, user_id, runtime, created_at, updated_at, status, name
        FROM sandboxes
        WHERE user_id = $1
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3
    `
    
    rows, err := s.DB.QueryContext(ctx, query, userID, limit, offset)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to list sandboxes: %w", err)
    }
    defer rows.Close()
    
    // Map to store sandbox IDs for label lookup
    sandboxIDs := make([]string, 0)
    sandboxes := make([]*types.SandboxMetadata, 0)
    
    for rows.Next() {
        var sandbox types.SandboxMetadata
        var name sql.NullString
        
        if err := rows.Scan(
            &sandbox.ID,
            &sandbox.UserID,
            &sandbox.Runtime,
            &sandbox.CreatedAt,
            &sandbox.UpdatedAt,
            &sandbox.Status,
            &name,
        ); err != nil {
            return nil, nil, fmt.Errorf("failed to scan sandbox row: %w", err)
        }
        
        if name.Valid {
            sandbox.Name = name.String
        }
        
        sandboxes = append(sandboxes, &sandbox)
        sandboxIDs = append(sandboxIDs, sandbox.ID)
    }
    
    if err := rows.Err(); err != nil {
        return nil, nil, fmt.Errorf("error iterating sandbox rows: %w", err)
    }
    
    // If we have sandboxes, fetch their labels
    if len(sandboxes) > 0 {
        // Create a map to store labels by sandbox ID
        sandboxLabels := make(map[string]map[string]string)
        
        // Build query with multiple sandbox IDs
        labelsQuery := `
            SELECT sandbox_id, key, value
            FROM sandbox_labels
            WHERE sandbox_id = ANY($1)
        `
        
        labelRows, err := s.DB.QueryContext(ctx, labelsQuery, sandboxIDs)
        if err != nil && err != sql.ErrNoRows {
            return nil, nil, fmt.Errorf("failed to get sandbox labels: %w", err)
        }
        
        if err != sql.ErrNoRows {
            defer labelRows.Close()
            
            for labelRows.Next() {
                var sandboxID, key, value string
                if err := labelRows.Scan(&sandboxID, &key, &value); err != nil {
                    return nil, nil, fmt.Errorf("failed to scan sandbox label: %w", err)
                }
                
                if _, exists := sandboxLabels[sandboxID]; !exists {
                    sandboxLabels[sandboxID] = make(map[string]string)
                }
                
                sandboxLabels[sandboxID][key] = value
            }
            
            if err := labelRows.Err(); err != nil {
                return nil, nil, fmt.Errorf("error iterating sandbox labels: %w", err)
            }
            
            // Assign labels to sandboxes
            for _, sandbox := range sandboxes {
                if labels, exists := sandboxLabels[sandbox.ID]; exists {
                    sandbox.Labels = labels
                }
            }
        }
    }
    
    return sandboxes, pagination, nil
}

// CheckResourceOwnership checks if a user owns a resource
func (s *Service) CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error) {
	var count int
	var query string

	switch resourceType {
	case "sandbox":
		query = "SELECT COUNT(*) FROM sandboxes WHERE id = $1 AND user_id = $2"
	case "warmpool":
		query = "SELECT COUNT(*) FROM warm_pools WHERE id = $1 AND user_id = $2"
	default:
		return false, fmt.Errorf("unsupported resource type: %s", resourceType)
	}

	err := s.DB.QueryRow(query, resourceID, userID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check resource ownership: %w", err)
	}

	return count > 0, nil
}

// CheckPermission checks if a user has permission to perform an action on a resource
func (s *Service) CheckPermission(userID, resourceType, resourceID, action string) (bool, error) {
	var count int
	query := `
		SELECT COUNT(*) FROM permissions
		WHERE user_id = $1
		AND resource_type = $2
		AND (resource_id = $3 OR resource_id = '*')
		AND (action = $4 OR action = '*')
	`

	err := s.DB.QueryRow(query, userID, resourceType, resourceID, action).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check permission: %w", err)
	}

	return count > 0, nil
}
