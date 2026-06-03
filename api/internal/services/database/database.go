// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lenaxia/llmsafespace/api/internal/config"
	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
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

func New(cfg *config.Config, log *logger.Logger) (*Service, error) {
	connString := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.Database,
		cfg.Database.SSLMode,
	)

	db, err := sql.Open("pgx", connString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

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
        SELECT id, username, email, password_hash, created_at, updated_at, active, role
        FROM users 
        WHERE id = $1
    `

	var user types.User

	err := s.DB.QueryRowContext(ctx, query, userID).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
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

// GetUserByEmail gets a user by email address
func (s *Service) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	query := `
        SELECT id, username, email, password_hash, created_at, updated_at, active, role
        FROM users 
        WHERE email = $1
    `

	var user types.User

	err := s.DB.QueryRowContext(ctx, query, email).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.Active,
		&user.Role,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user by email: %w", err)
	}

	return &user, nil
}

// CountUsers returns the total number of users in the system. Used by the
// auth Register flow to detect a fresh installation (count == 0) and
// auto-promote the first user to admin so a brand-new install has at least
// one administrator.
func (s *Service) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}
	return count, nil
}

// CreateUser creates a new user
func (s *Service) CreateUser(ctx context.Context, user *types.User) error {
	now := time.Now()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if user.UpdatedAt.IsZero() {
		user.UpdatedAt = now
	}

	// G8 (Epic 17): atomically promote the very first registrant to
	// admin in the same SQL statement so the count-then-insert race
	// is impossible. The CTE counts users `BEFORE` insert; if zero,
	// the role is forced to 'admin' regardless of the caller-supplied
	// value. If non-zero, the caller-supplied role wins (typically
	// 'user'). Postgres serializes the count + insert under the
	// row-level locks of the unique index on (email).
	query := `
		WITH existing AS (
			SELECT COUNT(*) AS n FROM users
		)
		INSERT INTO users (id, username, email, password_hash, created_at, updated_at, active, role)
		SELECT $1, $2, $3, $4, $5, $6, $7,
		       CASE WHEN existing.n = 0 THEN 'admin' ELSE $8 END
		FROM existing
		RETURNING role`

	var assignedRole string
	err := s.DB.QueryRowContext(ctx, query,
		user.ID,
		user.Username,
		user.Email,
		user.PasswordHash,
		user.CreatedAt,
		user.UpdatedAt,
		user.Active,
		user.Role,
	).Scan(&assignedRole)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	user.Role = assignedRole // reflect the actual role written
	return nil
}

// UpdateUser updates specific fields on a user record. Only non-nil fields are applied.
func (s *Service) UpdateUser(ctx context.Context, userID string, updates types.UserUpdates) error {
	query := "UPDATE users SET updated_at = NOW()"
	args := []interface{}{}
	i := 0

	if updates.Username != nil {
		i++
		query += fmt.Sprintf(", username = $%d", i)
		args = append(args, *updates.Username)
	}
	if updates.Email != nil {
		i++
		query += fmt.Sprintf(", email = $%d", i)
		args = append(args, *updates.Email)
	}
	if updates.Active != nil {
		i++
		query += fmt.Sprintf(", active = $%d", i)
		args = append(args, *updates.Active)
	}
	if updates.Role != nil {
		i++
		query += fmt.Sprintf(", role = $%d", i)
		args = append(args, *updates.Role)
	}
	if updates.PasswordHash != nil {
		i++
		query += fmt.Sprintf(", password_hash = $%d", i)
		args = append(args, *updates.PasswordHash)
	}

	if i == 0 {
		return nil
	}

	// Same pattern as credential_sets: WHERE clause is a literal
	// "WHERE id = $N"; user values bind via placeholders.
	query += fmt.Sprintf(" WHERE id = $%d", i+1) //nolint:gosec // G202: literal with placeholder bind
	args = append(args, userID)

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

// CheckResourceOwnership checks if a user owns a resource
func (s *Service) CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error) {
	var count int
	var query string

	switch resourceType {
	case "workspace":
		query = "SELECT COUNT(*) FROM workspaces WHERE id = $1 AND user_id = $2"
	default:
		return false, fmt.Errorf("unsupported resource type: %s", resourceType)
	}

	// QueryRow without context is intentional here: this method is called
	// from CheckResourceAccess which currently does not propagate context.
	// Plumbing ctx through CheckResourceAccess + the AuthService interface
	// is a larger refactor tracked separately.
	//nolint:noctx // see comment above
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

	// QueryRow without context: same caller-side limitation as
	// CheckResourceOwnership above. See note there.
	//nolint:noctx // see CheckResourceOwnership for context
	err := s.DB.QueryRow(query, userID, resourceType, resourceID, action).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check permission: %w", err)
	}

	return count > 0, nil
}

// GetWorkspace gets a workspace by ID.
func (s *Service) GetWorkspace(ctx context.Context, workspaceID string) (*types.WorkspaceMetadata, error) {
	if workspaceID == "" {
		return nil, nil
	}
	query := `
        SELECT w.id, w.user_id, w.name, w.runtime, w.storage_size, w.image_tag, w.agent_version, w.created_at, w.updated_at,
               COALESCE(s.pending_refresh, FALSE) AS agent_needs_refresh,
               s.last_credential_changed_at AS credentials_pending_since
        FROM workspaces w
        LEFT JOIN workspace_agent_state s ON s.workspace_id = w.id
        WHERE w.id = $1
    `
	var ws types.WorkspaceMetadata
	err := s.DB.QueryRowContext(ctx, query, workspaceID).Scan(
		&ws.ID,
		&ws.UserID,
		&ws.Name,
		&ws.Runtime,
		&ws.StorageSize,
		&ws.ImageTag,
		&ws.AgentVersion,
		&ws.CreatedAt,
		&ws.UpdatedAt,
		&ws.AgentNeedsRefresh,
		&ws.CredentialsPendingSince,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}
	return &ws, nil
}

// CreateWorkspace inserts a new workspace record.
func (s *Service) CreateWorkspace(ctx context.Context, workspace *types.WorkspaceMetadata) error {
	query := `
        INSERT INTO workspaces (id, user_id, name, runtime, storage_size, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
	now := time.Now()
	if workspace.CreatedAt.IsZero() {
		workspace.CreatedAt = now
	}
	if workspace.UpdatedAt.IsZero() {
		workspace.UpdatedAt = now
	}
	_, err := s.DB.ExecContext(ctx, query,
		workspace.ID,
		workspace.UserID,
		workspace.Name,
		workspace.Runtime,
		workspace.StorageSize,
		workspace.CreatedAt,
		workspace.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}
	return nil
}

// UpdateWorkspace updates specific fields on a workspace record.
func (s *Service) UpdateWorkspace(ctx context.Context, workspaceID string, updates types.WorkspaceUpdates) error {
	query := "UPDATE workspaces SET updated_at = NOW()"
	args := []interface{}{}
	i := 0
	if updates.Name != nil {
		i++
		query += fmt.Sprintf(", name = $%d", i)
		args = append(args, *updates.Name)
	}
	if updates.DefaultModel != nil {
		i++
		query += fmt.Sprintf(", default_model = $%d", i)
		args = append(args, *updates.DefaultModel)
	}
	if i == 0 {
		return nil
	}
	query += fmt.Sprintf(" WHERE id = $%d", i+1) //nolint:gosec // G202: literal with placeholder bind
	args = append(args, workspaceID)
	_, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update workspace: %w", err)
	}
	return nil
}

// DeleteWorkspace removes a workspace record.
func (s *Service) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM workspaces WHERE id = $1", workspaceID)
	if err != nil {
		return fmt.Errorf("failed to delete workspace: %w", err)
	}
	return nil
}

// SyncWorkspaceVersionInfo updates the image_tag and agent_version columns.
// Called when the workspace status is fetched and version info is available.
func (s *Service) SyncWorkspaceVersionInfo(ctx context.Context, workspaceID, imageTag, agentVersion string) {
	if workspaceID == "" || (imageTag == "" && agentVersion == "") {
		return
	}
	_, err := s.DB.ExecContext(ctx,
		"UPDATE workspaces SET image_tag = $1, agent_version = $2, updated_at = NOW() WHERE id = $3 AND deleted_at IS NULL",
		imageTag, agentVersion, workspaceID)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Error("failed to sync workspace version info to DB", err, "workspaceID", workspaceID)
		}
	}
}

// MarkWorkspaceDeleted soft-deletes a workspace by setting deleted_at and
// purges any user_secret_bindings rows pointing at it within a single
// transaction. The bindings table has no FK to workspaces.id (the column
// types differ historically) so a soft delete leaves orphan binding rows
// behind unless we clean up here explicitly. See Bug 11 in worklog 0085.
//
// The two writes are wrapped in a single transaction so an API-process
// crash between them cannot leave a soft-deleted workspace with orphan
// bindings (validator finding on Bug 11 follow-up).
func (s *Service) MarkWorkspaceDeleted(ctx context.Context, workspaceID string) {
	if workspaceID == "" {
		return
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Error("failed to begin tx for workspace soft-delete", err, "workspaceID", workspaceID)
		}
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx,
		"UPDATE workspaces SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL",
		workspaceID); err != nil {
		if s.Logger != nil {
			s.Logger.Error("failed to mark workspace deleted in DB", err, "workspaceID", workspaceID)
		}
		return
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM user_secret_bindings WHERE workspace_id = $1",
		workspaceID); err != nil {
		// Bindings DELETE failure rolls the entire transaction back:
		// neither the soft-delete nor the bindings purge land. The
		// caller's next reconcile retries from a clean state. We
		// prefer this over committing the soft-delete with orphan
		// bindings — the orphan rows are exactly the Bug-11 hazard
		// we are trying to eliminate.
		if s.Logger != nil {
			s.Logger.Warn("failed to delete user_secret_bindings for deleted workspace; rolling back entire tx",
				"workspaceID", workspaceID, "error", err.Error())
		}
		return
	}
	if err := tx.Commit(); err != nil {
		if s.Logger != nil {
			s.Logger.Error("failed to commit workspace soft-delete tx", err, "workspaceID", workspaceID)
		}
		return
	}
	committed = true
}

// ListWorkspaces lists workspaces for a user with pagination.
func (s *Service) ListWorkspaces(ctx context.Context, userID string, limit, offset int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error) {
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspaces WHERE user_id = $1 AND deleted_at IS NULL", userID).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("failed to count workspaces: %w", err)
	}
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
	if total == 0 {
		return []*types.WorkspaceMetadata{}, pagination, nil
	}
	rows, err := s.DB.QueryContext(ctx, `
        SELECT w.id, w.user_id, w.name, w.runtime, w.storage_size, w.image_tag, w.agent_version, w.created_at, w.updated_at,
               COALESCE(s.pending_refresh, FALSE) AS agent_needs_refresh,
               s.last_credential_changed_at AS credentials_pending_since
        FROM workspaces w
        LEFT JOIN workspace_agent_state s ON s.workspace_id = w.id
        WHERE w.user_id = $1 AND w.deleted_at IS NULL
        ORDER BY w.created_at DESC
        LIMIT $2 OFFSET $3
    `, userID, limit, offset)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list workspaces: %w", err)
	}
	defer func() { _ = rows.Close() }()
	workspaces := make([]*types.WorkspaceMetadata, 0)
	for rows.Next() {
		var ws types.WorkspaceMetadata
		if err := rows.Scan(
			&ws.ID, &ws.UserID, &ws.Name, &ws.Runtime,
			&ws.StorageSize,
			&ws.ImageTag, &ws.AgentVersion,
			&ws.CreatedAt, &ws.UpdatedAt,
			&ws.AgentNeedsRefresh, &ws.CredentialsPendingSince,
		); err != nil {
			return nil, nil, fmt.Errorf("failed to scan workspace row: %w", err)
		}
		workspaces = append(workspaces, &ws)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("error iterating workspace rows: %w", err)
	}
	return workspaces, pagination, nil
}

func (s *Service) CreateAPIKey(ctx context.Context, apiKey *types.APIKey) error {
	query := `
        INSERT INTO api_keys (id, user_id, key, name, active, created_at, expires_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
	var expiresAt interface{}
	if apiKey.ExpiresAt != nil {
		expiresAt = *apiKey.ExpiresAt
	}
	_, err := s.DB.ExecContext(ctx, query,
		apiKey.ID,
		apiKey.UserID,
		apiKey.Key,
		apiKey.Name,
		apiKey.Active,
		apiKey.CreatedAt,
		expiresAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create api key: %w", err)
	}
	return nil
}

func (s *Service) ListAPIKeys(ctx context.Context, userID string) ([]*types.APIKey, error) {
	query := `
        SELECT id, user_id, key, name, active, created_at, expires_at
        FROM api_keys
        WHERE user_id = $1
        ORDER BY created_at DESC
    `
	rows, err := s.DB.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	keys := make([]*types.APIKey, 0)
	for rows.Next() {
		var k types.APIKey
		var keyStr string
		var expiresAt sql.NullTime
		if err := rows.Scan(&k.ID, new(string), &keyStr, &k.Name, &k.Active, &k.CreatedAt, &expiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan api key: %w", err)
		}
		k.Prefix = "lsp_"
		if expiresAt.Valid {
			k.ExpiresAt = &expiresAt.Time
		}
		keys = append(keys, &k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating api keys: %w", err)
	}
	return keys, nil
}

func (s *Service) GetAPIKey(ctx context.Context, userID, keyID string) (*types.APIKey, error) {
	query := `
        SELECT id, key, name, active, created_at, expires_at
        FROM api_keys
        WHERE id = $1 AND user_id = $2
    `
	var k types.APIKey
	var keyStr string
	var expiresAt sql.NullTime
	err := s.DB.QueryRowContext(ctx, query, keyID, userID).Scan(
		&k.ID, &keyStr, &k.Name, &k.Active, &k.CreatedAt, &expiresAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get api key: %w", err)
	}
	k.Prefix = "lsp_"
	if expiresAt.Valid {
		k.ExpiresAt = &expiresAt.Time
	}
	return &k, nil
}

func (s *Service) DeleteAPIKey(ctx context.Context, userID, keyID string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1 AND user_id = $2", keyID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete api key: %w", err)
	}
	return nil
}

// --- Session Index DB methods (Phase A) ---

func (s *Service) ListSessionIndex(ctx context.Context, workspaceID string) ([]types.SessionListItem, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT session_id, title, parent_session_id, last_message_at, message_count
		 FROM session_index WHERE workspace_id = $1
		 ORDER BY last_message_at DESC NULLS LAST LIMIT 100`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]types.SessionListItem, 0)
	for rows.Next() {
		var item types.SessionListItem
		var title sql.NullString
		var parentID sql.NullString
		var lastMsg sql.NullTime
		if err := rows.Scan(&item.ID, &title, &parentID, &lastMsg, &item.MessageCount); err != nil {
			return nil, err
		}
		if title.Valid {
			item.Title = title.String
		}
		if parentID.Valid {
			item.ParentID = parentID.String
		}
		if lastMsg.Valid {
			t := lastMsg.Time
			item.LastMessageAt = &t
		}
		item.Status = "idle"
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) DeleteSessionIndex(ctx context.Context, workspaceID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM session_index WHERE workspace_id = $1`, workspaceID)
	return err
}

func (s *Service) UpsertSessionMessage(ctx context.Context, workspaceID, sessionID string, at time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO session_index (workspace_id, session_id, last_message_at, message_count, updated_at)
		 VALUES ($1, $2, $3, 1, NOW())
		 ON CONFLICT (workspace_id, session_id) DO UPDATE SET
		   last_message_at = EXCLUDED.last_message_at,
		   message_count = session_index.message_count + 1,
		   updated_at = NOW()`, workspaceID, sessionID, at)
	return err
}

func (s *Service) UpsertSessionTitle(ctx context.Context, workspaceID, sessionID, title string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO session_index (workspace_id, session_id, title, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (workspace_id, session_id) DO UPDATE SET
		   title = EXCLUDED.title,
		   updated_at = NOW()`, workspaceID, sessionID, title)
	return err
}

// UpsertSessionParent records (or refreshes) the parent_session_id for a
// session. Used to mirror opencode subagent (subtask) parent links into the
// sidebar's session_index so the UI can render the hierarchy without
// round-tripping the agent.
//
// Idempotent: passing the same parentID is a no-op. We deliberately do not
// guard against parentID changes — opencode never re-parents a session in
// practice, and an UPDATE-on-conflict path costs less than a SELECT-then-
// UPDATE round trip.
func (s *Service) UpsertSessionParent(ctx context.Context, workspaceID, sessionID, parentID string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO session_index (workspace_id, session_id, parent_session_id, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (workspace_id, session_id) DO UPDATE SET
		   parent_session_id = EXCLUDED.parent_session_id,
		   updated_at = NOW()`, workspaceID, sessionID, parentID)
	return err
}

// BeginTx starts a new database transaction. Used by handlers that need
// multi-statement atomicity (e.g., AgentReloadHandler's SELECT FOR UPDATE + UPSERT).
func (s *Service) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return s.DB.BeginTx(ctx, opts)
}

// MarkCredentialChanged flips a workspace into "credentials staged, reload needed" state.
// Uses a single auto-commit UPSERT (no external transaction parameter) because
// the binding write (PgSecretStore, pgxpool) and this write (*sql.DB) use
// incompatible connection pools — cross-pool transactions are impossible.
func (s *Service) MarkCredentialChanged(ctx context.Context, workspaceID string) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO workspace_agent_state
			(workspace_id, last_credential_changed_at, pending_refresh, updated_at)
		VALUES ($1, NOW(), TRUE, NOW())
		ON CONFLICT (workspace_id) DO UPDATE SET
			last_credential_changed_at = NOW(),
			pending_refresh = TRUE,
			updated_at = NOW()
	`, workspaceID)
	if err != nil {
		return fmt.Errorf("mark credential changed: %w", err)
	}
	return nil
}

// GetLastCredentialChangedAt returns the most recent credential-changed
// timestamp for the workspace, or the zero time if no row exists.
func (s *Service) GetLastCredentialChangedAt(ctx context.Context, workspaceID string) (time.Time, error) {
	var t time.Time
	err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(last_credential_changed_at, '1970-01-01') FROM workspace_agent_state WHERE workspace_id = $1`,
		workspaceID,
	).Scan(&t)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("get last credential changed at: %w", err)
	}
	return t, nil
}

// MarkAgentReloaded clears pending_refresh after a successful dispose.
// Uses SELECT FOR UPDATE to serialise against concurrent MarkCredentialChanged.
// priorChangedAt is captured BEFORE dispose; if a new credential was staged
// during the dispose window, pending_refresh stays true.
// Returns the DB-clock timestamp written to last_agent_disposed_at.
func (s *Service) MarkAgentReloaded(ctx context.Context, tx *sql.Tx, workspaceID string, priorChangedAt time.Time) (time.Time, error) {
	var currentChangedAt time.Time
	err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(last_credential_changed_at, '1970-01-01')
		 FROM workspace_agent_state
		 WHERE workspace_id = $1
		 FOR UPDATE`,
		workspaceID,
	).Scan(&currentChangedAt)
	if err == sql.ErrNoRows {
		return time.Time{}, apierrors.ErrNoAgentStateRow
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("lock workspace_agent_state: %w", err)
	}

	// pending_refresh stays true if a credential was staged during dispose window.
	newPendingRefresh := currentChangedAt.After(priorChangedAt)

	var disposedAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO workspace_agent_state
			(workspace_id, last_agent_disposed_at, pending_refresh, updated_at)
		VALUES ($1, NOW(), $2, NOW())
		ON CONFLICT (workspace_id) DO UPDATE SET
			last_agent_disposed_at = NOW(),
			pending_refresh = $2,
			updated_at = NOW()
		RETURNING last_agent_disposed_at
	`, workspaceID, newPendingRefresh).Scan(&disposedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("mark agent reloaded: %w", err)
	}
	return disposedAt, nil
}
