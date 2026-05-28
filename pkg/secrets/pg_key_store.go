package secrets

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgKeyStore implements KeyStore using PostgreSQL.
type PgKeyStore struct {
	pool *pgxpool.Pool
}

// NewPgKeyStore creates a new PostgreSQL-backed key store.
func NewPgKeyStore(pool *pgxpool.Pool) *PgKeyStore {
	return &PgKeyStore{pool: pool}
}

func (s *PgKeyStore) GetUserKey(ctx context.Context, userID string) (*UserKeyRecord, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT user_id, key_version, wrapped_dek, wrapped_dek_recovery, salt, recovery_salt, created_at, rotated_at
		 FROM user_keys WHERE user_id = $1`, userID)

	var r UserKeyRecord
	err := row.Scan(&r.UserID, &r.KeyVersion, &r.WrappedDEK, &r.WrappedDEKRecovery, &r.Salt, &r.RecoverySalt, &r.CreatedAt, &r.RotatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query user_keys: %w", err)
	}
	return &r, nil
}

func (s *PgKeyStore) CreateUserKey(ctx context.Context, record *UserKeyRecord) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO user_keys (user_id, key_version, wrapped_dek, wrapped_dek_recovery, salt, recovery_salt, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		record.UserID, record.KeyVersion, record.WrappedDEK, record.WrappedDEKRecovery, record.Salt, record.RecoverySalt, record.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert user_keys: %w", err)
	}
	return nil
}

func (s *PgKeyStore) UpdateWrappedDEK(ctx context.Context, userID string, wrappedDEK []byte, salt []byte, keyVersion int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE user_keys SET wrapped_dek = $1, salt = $2, key_version = $3, rotated_at = NOW() WHERE user_id = $4`,
		wrappedDEK, salt, keyVersion, userID)
	if err != nil {
		return fmt.Errorf("update wrapped_dek: %w", err)
	}
	return nil
}

func (s *PgKeyStore) UpdateWrappedDEKRecovery(ctx context.Context, userID string, wrappedDEKRecovery []byte, recoverySalt []byte) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE user_keys SET wrapped_dek_recovery = $1, recovery_salt = $2 WHERE user_id = $3`,
		wrappedDEKRecovery, recoverySalt, userID)
	if err != nil {
		return fmt.Errorf("update wrapped_dek_recovery: %w", err)
	}
	return nil
}
