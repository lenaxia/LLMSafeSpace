package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// Service handles database operations
type Service struct {
	logger *logger.Logger
	config *config.Config
	db     *sql.DB
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
		logger: log,
		config: cfg,
		db:     db,
	}, nil
}

// Start starts the database service
func (s *Service) Start() error {
	s.logger.Info("Database service started")
	return nil
}

// Stop stops the database service
func (s *Service) Stop() error {
	s.logger.Info("Stopping database service")
	return s.db.Close()
}

// Ping checks the database connection
func (s *Service) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// GetUserIDByAPIKey gets the user ID associated with an API key
func (s *Service) GetUserIDByAPIKey(apiKey string) (string, error) {
	var userID string
	err := s.db.QueryRow("SELECT user_id FROM api_keys WHERE key = $1 AND active = true", apiKey).Scan(&userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("failed to get user ID by API key: %w", err)
	}
	return userID, nil
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

	err := s.db.QueryRow(query, resourceID, userID).Scan(&count)
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

	err := s.db.QueryRow(query, userID, resourceType, resourceID, action).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check permission: %w", err)
	}

	return count > 0, nil
}

// CreateSandboxMetadata stores sandbox metadata in the database
func (s *Service) CreateSandboxMetadata(ctx context.Context, sandboxID, userID, runtime string) error {
	query := `
		INSERT INTO sandboxes (id, user_id, runtime, created_at)
		VALUES ($1, $2, $3, $4)
	`

	_, err := s.db.ExecContext(ctx, query, sandboxID, userID, runtime, time.Now())
	if err != nil {
		return fmt.Errorf("failed to create sandbox metadata: %w", err)
	}

	return nil
}

// DeleteSandboxMetadata deletes sandbox metadata from the database
func (s *Service) DeleteSandboxMetadata(ctx context.Context, sandboxID string) error {
	query := `DELETE FROM sandboxes WHERE id = $1`

	_, err := s.db.ExecContext(ctx, query, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox metadata: %w", err)
	}

	return nil
}

// GetSandboxMetadata gets sandbox metadata from the database
func (s *Service) GetSandboxMetadata(ctx context.Context, sandboxID string) (map[string]interface{}, error) {
	query := `
		SELECT id, user_id, runtime, created_at
		FROM sandboxes
		WHERE id = $1
	`

	var id, userID, runtime string
	var createdAt time.Time

	err := s.db.QueryRowContext(ctx, query, sandboxID).Scan(&id, &userID, &runtime, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get sandbox metadata: %w", err)
	}

	return map[string]interface{}{
		"id":        id,
		"user_id":   userID,
		"runtime":   runtime,
		"created_at": createdAt,
	}, nil
}

// ListSandboxes lists sandboxes for a user
func (s *Service) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	query := `
		SELECT id, runtime, created_at
		FROM sandboxes
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}
	defer rows.Close()

	var sandboxes []map[string]interface{}
	for rows.Next() {
		var id, runtime string
		var createdAt time.Time

		if err := rows.Scan(&id, &runtime, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan sandbox row: %w", err)
		}

		sandboxes = append(sandboxes, map[string]interface{}{
			"id":        id,
			"runtime":   runtime,
			"created_at": createdAt,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating sandbox rows: %w", err)
	}

	return sandboxes, nil
}
