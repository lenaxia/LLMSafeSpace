package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgSecretStore implements SecretStore using PostgreSQL.
type PgSecretStore struct {
	pool *pgxpool.Pool
}

// NewPgSecretStore creates a new PostgreSQL-backed secret store.
func NewPgSecretStore(pool *pgxpool.Pool) *PgSecretStore {
	return &PgSecretStore{pool: pool}
}

func (s *PgSecretStore) CreateSecret(ctx context.Context, secret *UserSecret) error {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO user_secrets (user_id, name, type, ciphertext, key_version, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at, updated_at`,
		secret.UserID, secret.Name, secret.Type, secret.Ciphertext, secret.KeyVersion, secret.Metadata)

	return row.Scan(&secret.ID, &secret.CreatedAt, &secret.UpdatedAt)
}

func (s *PgSecretStore) GetSecret(ctx context.Context, userID, secretID string) (*UserSecret, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, user_id, name, type, ciphertext, key_version, metadata, created_at, updated_at
		 FROM user_secrets WHERE id = $1 AND user_id = $2`, secretID, userID)

	var sec UserSecret
	err := row.Scan(&sec.ID, &sec.UserID, &sec.Name, &sec.Type, &sec.Ciphertext, &sec.KeyVersion, &sec.Metadata, &sec.CreatedAt, &sec.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query user_secrets: %w", err)
	}
	return &sec, nil
}

func (s *PgSecretStore) GetSecretByName(ctx context.Context, userID, name string) (*UserSecret, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, user_id, name, type, ciphertext, key_version, metadata, created_at, updated_at
		 FROM user_secrets WHERE user_id = $1 AND name = $2`, userID, name)

	var sec UserSecret
	err := row.Scan(&sec.ID, &sec.UserID, &sec.Name, &sec.Type, &sec.Ciphertext, &sec.KeyVersion, &sec.Metadata, &sec.CreatedAt, &sec.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query user_secrets by name: %w", err)
	}
	return &sec, nil
}

func (s *PgSecretStore) ListSecrets(ctx context.Context, userID string) ([]*UserSecret, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, name, type, ciphertext, key_version, metadata, created_at, updated_at
		 FROM user_secrets WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user_secrets: %w", err)
	}
	defer rows.Close()

	var secrets []*UserSecret
	for rows.Next() {
		var sec UserSecret
		if err := rows.Scan(&sec.ID, &sec.UserID, &sec.Name, &sec.Type, &sec.Ciphertext, &sec.KeyVersion, &sec.Metadata, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user_secrets: %w", err)
		}
		secrets = append(secrets, &sec)
	}
	return secrets, rows.Err()
}

func (s *PgSecretStore) UpdateSecret(ctx context.Context, secret *UserSecret) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE user_secrets SET ciphertext = $1, key_version = $2, metadata = $3, updated_at = $4
		 WHERE id = $5 AND user_id = $6`,
		secret.Ciphertext, secret.KeyVersion, secret.Metadata, secret.UpdatedAt, secret.ID, secret.UserID)
	return err
}

// pgxTxKey is a private context key used to thread an active *pgx.Tx
// through callbacks invoked from inside ReEncryptUserSecrets. The
// PgKeyStore reads this key when present so its UpdateWrappedDEK runs
// inside the same transaction as the secret re-encryption (Bug 9 fix
// in worklog 0094 — the user_keys update and the user_secrets walk
// must be atomic; without this they were two separate statements with
// a partial-failure window between them).
type pgxTxKey struct{}

// withTx returns a context carrying tx for downstream stores to detect.
func withTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, pgxTxKey{}, tx)
}

// txFromContext returns the tx threaded into ctx, if any.
func txFromContext(ctx context.Context) pgx.Tx {
	if tx, ok := ctx.Value(pgxTxKey{}).(pgx.Tx); ok {
		return tx
	}
	return nil
}

// ReEncryptUserSecrets walks every user_secrets row owned by userID
// inside a single SERIALIZABLE transaction, retrying on serialization
// failure. After the walk completes the commit closure runs in the
// same transaction so callers (KeyService.RotateKeyWithPassword) can
// update related rows (e.g. user_keys.wrapped_dek) atomically. If
// commit returns non-nil the entire transaction rolls back: secrets
// stay encrypted with the old DEK and user_keys stays unchanged.
//
// A row-count cap of maxRotateRows is enforced to prevent a malicious
// or pathological account from holding the rotation transaction open
// indefinitely.
//
// See Bug 9 in worklog 0085 / 0086.
func (s *PgSecretStore) ReEncryptUserSecrets(
	ctx context.Context,
	userID string,
	newKeyVersion int,
	transform func([]byte) ([]byte, error),
	commit func(ctx context.Context) error,
) error {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err := s.runReEncryptTx(ctx, userID, newKeyVersion, transform, commit)
		if err == nil {
			return nil
		}
		// pgx 5.x exposes serialization_failure as SQLSTATE "40001"; we
		// retry transparently a small bounded number of times.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40001" {
			lastErr = err
			continue
		}
		return err
	}
	return fmt.Errorf("rotate-key: serialization failure persisted after %d retries: %w", maxRetries, lastErr)
}

// maxRotateRows is the largest number of secrets a single rotation may
// touch. Set high enough that no realistic user hits it (~16k secrets)
// but low enough that a runaway never holds the rotation tx open for
// minutes.
const maxRotateRows = 16384

func (s *PgSecretStore) runReEncryptTx(
	ctx context.Context,
	userID string,
	newKeyVersion int,
	transform func([]byte) ([]byte, error),
	commit func(ctx context.Context) error,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Plain SELECT (no FOR UPDATE) — SERIALIZABLE / SSI uses predicate
	// locks and detects conflicting writes via 40001 abort. Adding
	// FOR UPDATE on top would acquire physical row locks that SSI
	// does not need and increases the abort rate when concurrent
	// secret CRUD touches the same user. The retry loop in the caller
	// handles 40001 transparently.
	rows, err := tx.Query(ctx,
		`SELECT id, ciphertext FROM user_secrets WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("select user_secrets: %w", err)
	}

	type pending struct {
		id            string
		newCiphertext []byte
	}
	pendings := make([]pending, 0, 64)
	for rows.Next() {
		if len(pendings) >= maxRotateRows {
			rows.Close()
			return fmt.Errorf("rotate-key: too many secrets (>%d) for user %s", maxRotateRows, userID)
		}
		var id string
		var ct []byte
		if err := rows.Scan(&id, &ct); err != nil {
			rows.Close()
			return fmt.Errorf("scan row: %w", err)
		}
		newCT, err := transform(ct)
		if err != nil {
			rows.Close()
			return fmt.Errorf("transform secret %s: %w", id, err)
		}
		pendings = append(pendings, pending{id: id, newCiphertext: newCT})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rows: %w", err)
	}

	now := time.Now()
	for _, p := range pendings {
		if _, err := tx.Exec(ctx,
			`UPDATE user_secrets SET ciphertext = $1, key_version = $2, updated_at = $3
			 WHERE id = $4 AND user_id = $5`,
			p.newCiphertext, newKeyVersion, now, p.id, userID); err != nil {
			return fmt.Errorf("update secret %s: %w", p.id, err)
		}
	}

	// Run caller's atomic-commit hook inside the same tx so user_keys
	// updates land or roll back together with the re-encrypted rows.
	if commit != nil {
		if err := commit(withTx(ctx, tx)); err != nil {
			return fmt.Errorf("commit hook: %w", err)
		}
	}

	return tx.Commit(ctx)
}

func (s *PgSecretStore) DeleteSecret(ctx context.Context, userID, secretID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM user_secrets WHERE id = $1 AND user_id = $2`, secretID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("secret %s not found", secretID)
	}
	return nil
}

func (s *PgSecretStore) SetBindings(ctx context.Context, workspaceID string, secretIDs []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Remove existing bindings for this workspace
	_, err = tx.Exec(ctx, `DELETE FROM user_secret_bindings WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return err
	}

	// Insert new bindings
	for _, sid := range secretIDs {
		_, err = tx.Exec(ctx,
			`INSERT INTO user_secret_bindings (secret_id, workspace_id) VALUES ($1, $2)`,
			sid, workspaceID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *PgSecretStore) GetBindings(ctx context.Context, workspaceID string) ([]*UserSecret, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT s.id, s.user_id, s.name, s.type, s.ciphertext, s.key_version, s.metadata, s.created_at, s.updated_at
		 FROM user_secrets s
		 JOIN user_secret_bindings b ON b.secret_id = s.id
		 WHERE b.workspace_id = $1
		 ORDER BY s.name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var secrets []*UserSecret
	for rows.Next() {
		var sec UserSecret
		if err := rows.Scan(&sec.ID, &sec.UserID, &sec.Name, &sec.Type, &sec.Ciphertext, &sec.KeyVersion, &sec.Metadata, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
			return nil, err
		}
		secrets = append(secrets, &sec)
	}
	return secrets, rows.Err()
}

func (s *PgSecretStore) GetBindingsForSecret(ctx context.Context, secretID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT workspace_id FROM user_secret_bindings WHERE secret_id = $1`, secretID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workspaces []string
	for rows.Next() {
		var wsID string
		if err := rows.Scan(&wsID); err != nil {
			return nil, err
		}
		workspaces = append(workspaces, wsID)
	}
	return workspaces, rows.Err()
}

func (s *PgSecretStore) LogAudit(ctx context.Context, entry *AuditEntry) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO secret_audit_log (user_id, action, secret_id, workspace_id, metadata, timestamp)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		entry.UserID, entry.Action, entry.SecretID, entry.WorkspaceID, entry.Metadata, entry.Timestamp)
	return err
}

func (s *PgSecretStore) QueryAudit(ctx context.Context, userID string, query AuditQuery) ([]*AuditEntry, error) {
	sql := `SELECT id, user_id, action, secret_id, workspace_id, metadata, timestamp
		    FROM secret_audit_log WHERE user_id = $1`
	args := []interface{}{userID}
	argIdx := 2

	if query.Action != "" {
		sql += fmt.Sprintf(" AND action = $%d", argIdx)
		args = append(args, query.Action)
		argIdx++
	}
	if query.SecretID != "" {
		sql += fmt.Sprintf(" AND secret_id = $%d", argIdx)
		args = append(args, query.SecretID)
		argIdx++
	}
	if query.WorkspaceID != "" {
		sql += fmt.Sprintf(" AND workspace_id = $%d", argIdx)
		args = append(args, query.WorkspaceID)
		argIdx++
	}
	if query.Since != nil {
		sql += fmt.Sprintf(" AND timestamp >= $%d", argIdx)
		args = append(args, *query.Since)
		argIdx++
	}
	if query.Until != nil {
		sql += fmt.Sprintf(" AND timestamp <= $%d", argIdx)
		args = append(args, *query.Until)
		argIdx++
	}

	sql += " ORDER BY timestamp DESC"

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	sql += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, limit)
	argIdx++

	if query.Offset > 0 {
		sql += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, query.Offset)
	}

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var secretID, workspaceID *string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Action, &secretID, &workspaceID, &e.Metadata, &e.Timestamp); err != nil {
			return nil, err
		}
		e.SecretID = secretID
		e.WorkspaceID = workspaceID
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// AsyncAuditLogger wraps a SecretStore and logs audit entries asynchronously.
type AsyncAuditLogger struct {
	store SecretStore
	ch    chan *AuditEntry
	done  chan struct{}
}

// NewAsyncAuditLogger creates an async audit logger with a buffered channel.
func NewAsyncAuditLogger(store SecretStore, bufSize int) *AsyncAuditLogger {
	l := &AsyncAuditLogger{
		store: store,
		ch:    make(chan *AuditEntry, bufSize),
		done:  make(chan struct{}),
	}
	go l.run()
	return l
}

func (l *AsyncAuditLogger) run() {
	for entry := range l.ch {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = l.store.LogAudit(ctx, entry)
		cancel()
	}
	close(l.done)
}

// Log sends an audit entry to the async channel.
func (l *AsyncAuditLogger) Log(entry *AuditEntry) {
	select {
	case l.ch <- entry:
	default:
		// Channel full — drop entry rather than block hot path
	}
}

// Stop drains the channel and waits for completion.
func (l *AsyncAuditLogger) Stop() {
	close(l.ch)
	<-l.done
}
