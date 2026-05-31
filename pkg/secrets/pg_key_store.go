package secrets

import (
	"context"
	"errors"
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
	if errors.Is(err, pgx.ErrNoRows) {
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

// UpdateWrappedDEK updates the wrapped DEK for a user. When the
// context carries an active *pgx.Tx (threaded through by
// SecretStore.ReEncryptUserSecrets via withTx), the UPDATE runs inside
// that transaction so the user_keys row and the user_secrets re-encrypt
// commit or roll back atomically. Otherwise the UPDATE runs on the
// pool directly. See Bug 9 in worklog 0094.
func (s *PgKeyStore) UpdateWrappedDEK(ctx context.Context, userID string, wrappedDEK []byte, salt []byte, keyVersion int) error {
	const sqlStmt = `UPDATE user_keys SET wrapped_dek = $1, salt = $2, key_version = $3, rotated_at = NOW() WHERE user_id = $4`
	if tx := txFromContext(ctx); tx != nil {
		if _, err := tx.Exec(ctx, sqlStmt, wrappedDEK, salt, keyVersion, userID); err != nil {
			return fmt.Errorf("update wrapped_dek (tx): %w", err)
		}
		return nil
	}
	if _, err := s.pool.Exec(ctx, sqlStmt, wrappedDEK, salt, keyVersion, userID); err != nil {
		return fmt.Errorf("update wrapped_dek: %w", err)
	}
	return nil
}

// UpdateWrappedDEKRecovery updates the recovery-key wrap. Like
// UpdateWrappedDEK, the implementation honors an active *pgx.Tx
// threaded through the context (via withTx) so future callers that
// want to bundle a recovery-key rotation into the same atomic unit as
// the password-key rotation can do so. No current caller does, but
// the parity with UpdateWrappedDEK closes a latent footgun.
func (s *PgKeyStore) UpdateWrappedDEKRecovery(ctx context.Context, userID string, wrappedDEKRecovery []byte, recoverySalt []byte) error {
	const sqlStmt = `UPDATE user_keys SET wrapped_dek_recovery = $1, recovery_salt = $2 WHERE user_id = $3`
	if tx := txFromContext(ctx); tx != nil {
		if _, err := tx.Exec(ctx, sqlStmt, wrappedDEKRecovery, recoverySalt, userID); err != nil {
			return fmt.Errorf("update wrapped_dek_recovery (tx): %w", err)
		}
		return nil
	}
	if _, err := s.pool.Exec(ctx, sqlStmt, wrappedDEKRecovery, recoverySalt, userID); err != nil {
		return fmt.Errorf("update wrapped_dek_recovery: %w", err)
	}
	return nil
}
