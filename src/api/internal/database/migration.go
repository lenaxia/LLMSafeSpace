package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
)

// Migration represents a database migration
type Migration struct {
	Version   int
	Name      string
	SQL       string
	Timestamp time.Time
}

// MigrationService handles database migrations
type MigrationService struct {
	db     *database.Service
	logger *logger.Logger
}

// NewMigrationService creates a new migration service
func NewMigrationService(db *database.Service, logger *logger.Logger) *MigrationService {
	return &MigrationService{
		db:     db,
		logger: logger,
	}
}

// LoadMigrationsFromFS loads migrations from an embedded filesystem
func (m *MigrationService) LoadMigrationsFromFS(migrationFS embed.FS) ([]Migration, error) {
	var migrations []Migration

	// Read migration files from the embedded filesystem
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Parse version and name from filename (format: V1__description.sql)
		filename := entry.Name()
		parts := strings.SplitN(filename, "__", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "V") {
			m.logger.Warn("Skipping invalid migration filename", "filename", filename)
			continue
		}

		versionStr := strings.TrimPrefix(parts[0], "V")
		var version int
		_, err := fmt.Sscanf(versionStr, "%d", &version)
		if err != nil {
			m.logger.Warn("Skipping migration with invalid version", "filename", filename, "error", err)
			continue
		}

		// Read migration content
		content, err := fs.ReadFile(migrationFS, path.Join("migrations", filename))
		if err != nil {
			return nil, fmt.Errorf("failed to read migration file %s: %w", filename, err)
		}

		migrations = append(migrations, Migration{
			Version:   version,
			Name:      strings.TrimSuffix(parts[1], ".sql"),
			SQL:       string(content),
			Timestamp: time.Now(),
		})
	}

	// Sort migrations by version
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// ApplyMigrations applies all migrations to the database
func (m *MigrationService) ApplyMigrations(ctx context.Context, migrations []Migration) error {
	// Create migrations table if it doesn't exist
	_, err := m.db.DB().ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMP NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get applied migrations
	rows, err := m.db.DB().QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("failed to query migrations: %w", err)
	}
	defer rows.Close()

	appliedVersions := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("failed to scan migration version: %w", err)
		}
		appliedVersions[version] = true
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating migrations: %w", err)
	}

	// Apply pending migrations
	for _, migration := range migrations {
		if appliedVersions[migration.Version] {
			m.logger.Debug("Skipping already applied migration", "version", migration.Version, "name", migration.Name)
			continue
		}

		m.logger.Info("Applying migration", "version", migration.Version, "name", migration.Name)

		// Start a transaction for the migration
		tx, err := m.db.DB.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to start transaction for migration %d: %w", migration.Version, err)
		}

		// Apply the migration
		_, err = tx.ExecContext(ctx, migration.SQL)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to apply migration %d: %w", migration.Version, err)
		}

		// Record the migration
		_, err = tx.ExecContext(ctx, `
			INSERT INTO schema_migrations (version, name, applied_at)
			VALUES ($1, $2, $3)
		`, migration.Version, migration.Name, time.Now())
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %d: %w", migration.Version, err)
		}

		// Commit the transaction
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", migration.Version, err)
		}

		m.logger.Info("Successfully applied migration", "version", migration.Version, "name", migration.Name)
	}

	return nil
}
