package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/kubernetes"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// dbKeyStoreAdapter is an in-memory KeyStore used by app-level wiring
// tests so they don't need a Postgres instance. It is NOT used in
// production: app.New refuses to start if pgxpool initialisation fails.
//
// Concurrency: guarded by a mutex so future tests running with
// t.Parallel() do not race; correctness is otherwise irrelevant
// because every call within a single goroutine reads/writes the same
// map atomically under the lock.
type dbKeyStoreAdapter struct {
	mu      sync.Mutex
	db      interfaces.DatabaseService
	memKeys map[string]*secrets.UserKeyRecord
}

func (a *dbKeyStoreAdapter) GetUserKey(_ context.Context, userID string) (*secrets.UserKeyRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.memKeys == nil {
		return nil, nil
	}
	r, ok := a.memKeys[userID]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (a *dbKeyStoreAdapter) CreateUserKey(_ context.Context, record *secrets.UserKeyRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.memKeys == nil {
		a.memKeys = make(map[string]*secrets.UserKeyRecord)
	}
	cp := *record
	a.memKeys[record.UserID] = &cp
	return nil
}

func (a *dbKeyStoreAdapter) UpdateWrappedDEK(_ context.Context, userID string, wrappedDEK []byte, salt []byte, keyVersion int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.memKeys == nil {
		return nil
	}
	r, ok := a.memKeys[userID]
	if !ok {
		return nil
	}
	r.WrappedDEK = wrappedDEK
	r.Salt = salt
	r.KeyVersion = keyVersion
	return nil
}

func (a *dbKeyStoreAdapter) UpdateWrappedDEKRecovery(_ context.Context, userID string, wrappedDEKRecovery []byte, recoverySalt []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.memKeys == nil {
		return nil
	}
	r, ok := a.memKeys[userID]
	if !ok {
		return nil
	}
	r.WrappedDEKRecovery = wrappedDEKRecovery
	r.RecoverySalt = recoverySalt
	return nil
}

// dbSecretStoreAdapter is an in-memory SecretStore used by app-level
// wiring tests. NOT used in production: app.New refuses to start if
// pgxpool initialisation fails, which would otherwise be the only
// caller of this adapter.
//
// Concurrency: guarded by a mutex so future tests running with
// t.Parallel() do not race. The audit slice is bounded so a long test
// run does not grow without bound.
type dbSecretStoreAdapter struct {
	mu       sync.Mutex
	db       interfaces.DatabaseService
	secrets  map[string]*secrets.UserSecret
	bindings map[string][]string
	audit    []*secrets.AuditEntry
}

// maxAdapterAuditEntries caps the in-memory audit slice. Production
// uses pg-backed audit storage so this only affects test-suite memory
// footprint; without the cap a long test run accumulates audit entries
// without bound.
const maxAdapterAuditEntries = 4096

// init lazy-initialises maps. Caller must already hold a.mu.
func (a *dbSecretStoreAdapter) init() {
	if a.secrets == nil {
		a.secrets = make(map[string]*secrets.UserSecret)
		a.bindings = make(map[string][]string)
	}
}

func (a *dbSecretStoreAdapter) CreateSecret(_ context.Context, secret *secrets.UserSecret) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	for _, s := range a.secrets {
		if s.UserID == secret.UserID && s.Name == secret.Name {
			return &duplicateErr{secret.Name}
		}
	}
	if secret.ID == "" {
		secret.ID = generateID()
	}
	cp := *secret
	a.secrets[secret.ID] = &cp
	return nil
}

func (a *dbSecretStoreAdapter) GetSecret(_ context.Context, userID, secretID string) (*secrets.UserSecret, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	s, ok := a.secrets[secretID]
	if !ok || s.UserID != userID {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (a *dbSecretStoreAdapter) GetSecretByName(_ context.Context, userID, name string) (*secrets.UserSecret, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	for _, s := range a.secrets {
		if s.UserID == userID && s.Name == name {
			cp := *s
			return &cp, nil
		}
	}
	return nil, nil
}

func (a *dbSecretStoreAdapter) ListSecrets(_ context.Context, userID string) ([]*secrets.UserSecret, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	var result []*secrets.UserSecret
	for _, s := range a.secrets {
		if s.UserID == userID {
			cp := *s
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (a *dbSecretStoreAdapter) UpdateSecret(_ context.Context, secret *secrets.UserSecret) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	if _, ok := a.secrets[secret.ID]; !ok {
		return &notFoundErr{secret.ID}
	}
	cp := *secret
	a.secrets[secret.ID] = &cp
	return nil
}

func (a *dbSecretStoreAdapter) ReEncryptUserSecrets(ctx context.Context, userID string, newKeyVersion int, transform func([]byte) ([]byte, error), commit func(context.Context) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	updates := make(map[string][]byte)
	for id, s := range a.secrets {
		if s.UserID != userID {
			continue
		}
		newCT, err := transform(s.Ciphertext)
		if err != nil {
			return err
		}
		updates[id] = newCT
	}
	if commit != nil {
		// Drop the lock so commit's downstream callbacks can re-enter
		// the adapter without deadlocking. The mutation phase below
		// re-acquires and re-validates each id (a concurrent
		// DeleteSecret during the unlocked window could have removed
		// rows from a.secrets — without re-validation we'd nil-deref
		// on the next line).
		a.mu.Unlock()
		err := commit(ctx)
		a.mu.Lock()
		if err != nil {
			return err
		}
	}
	for id, newCT := range updates {
		s, ok := a.secrets[id]
		if !ok || s == nil {
			// Concurrent DeleteSecret removed this row during the
			// commit-callback window. Skip silently — the secret no
			// longer exists, so nothing to re-encrypt.
			continue
		}
		s.Ciphertext = newCT
		s.KeyVersion = newKeyVersion
	}
	return nil
}

func (a *dbSecretStoreAdapter) DeleteSecret(_ context.Context, userID, secretID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	s, ok := a.secrets[secretID]
	if !ok || s.UserID != userID {
		return &notFoundErr{secretID}
	}
	delete(a.secrets, secretID)
	for wsID, sids := range a.bindings {
		var filtered []string
		for _, sid := range sids {
			if sid != secretID {
				filtered = append(filtered, sid)
			}
		}
		a.bindings[wsID] = filtered
	}
	return nil
}

func (a *dbSecretStoreAdapter) SetBindings(_ context.Context, workspaceID string, secretIDs []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	a.bindings[workspaceID] = secretIDs
	return nil
}

func (a *dbSecretStoreAdapter) AddBindings(_ context.Context, workspaceID string, secretIDs []string) error {
	if len(secretIDs) == 0 {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	existing := a.bindings[workspaceID]
	seen := make(map[string]struct{}, len(existing)+len(secretIDs))
	for _, id := range existing {
		seen[id] = struct{}{}
	}
	for _, id := range secretIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		existing = append(existing, id)
	}
	a.bindings[workspaceID] = existing
	return nil
}

func (a *dbSecretStoreAdapter) GetBindings(_ context.Context, workspaceID string) ([]*secrets.UserSecret, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	sids := a.bindings[workspaceID]
	var result []*secrets.UserSecret
	for _, sid := range sids {
		if s, ok := a.secrets[sid]; ok {
			cp := *s
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (a *dbSecretStoreAdapter) GetBindingsForSecret(_ context.Context, secretID string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	var ws []string
	for wsID, sids := range a.bindings {
		for _, sid := range sids {
			if sid == secretID {
				ws = append(ws, wsID)
			}
		}
	}
	return ws, nil
}

func (a *dbSecretStoreAdapter) LogAudit(_ context.Context, entry *secrets.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.audit = append(a.audit, entry)
	// Bound the slice — drop oldest when we exceed the cap. The cap
	// is large enough for any realistic test run but small enough to
	// keep memory bounded in long-running suites.
	if len(a.audit) > maxAdapterAuditEntries {
		drop := len(a.audit) - maxAdapterAuditEntries
		a.audit = a.audit[drop:]
	}
	return nil
}

func (a *dbSecretStoreAdapter) QueryAudit(_ context.Context, userID string, _ secrets.AuditQuery) ([]*secrets.AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var result []*secrets.AuditEntry
	for _, e := range a.audit {
		if e.UserID == userID {
			result = append(result, e)
		}
	}
	return result, nil
}

// workspaceOwnerVerifierAdapter implements secrets.WorkspaceOwnerVerifier
// against the api-side DatabaseService. Both "workspace not found"
// and "workspace owned by someone else" collapse to the single
// secrets.ErrWorkspaceNotOwned sentinel so the response shape does
// not differentiate between the two — preventing cross-user
// workspace-existence enumeration via the bindings API (validator
// pass-3 finding SO-1).
//
// DB-error events surface at Warn (validator pass-4 finding NEW-2)
// — without operator visibility, a Postgres outage would silently
// turn every binding op across the fleet into uniform 404s with
// zero log signal. Matches the precedent set by secretsPodIPResolver.
type workspaceOwnerVerifierAdapter struct {
	db     interfaces.DatabaseService
	logger pkginterfaces.LoggerInterface
}

func (a *workspaceOwnerVerifierAdapter) VerifyWorkspaceOwner(ctx context.Context, userID, workspaceID string) error {
	if a.db == nil || userID == "" || workspaceID == "" {
		return secrets.ErrWorkspaceNotOwned
	}
	meta, err := a.db.GetWorkspace(ctx, workspaceID)
	if err != nil {
		// Treat DB blip as not-owned to keep the response uniform.
		// Log at Warn so a real Postgres outage is operator-visible
		// rather than silently turning every binding op into a 404.
		if a.logger != nil {
			a.logger.Warn("workspaceOwnerVerifier: DB lookup failed; downgrading to not-owned",
				"workspaceID", workspaceID, "userID", userID, "error", err.Error())
		}
		return secrets.ErrWorkspaceNotOwned
	}
	if meta == nil || meta.UserID != userID {
		return secrets.ErrWorkspaceNotOwned
	}
	return nil
}

type duplicateErr struct{ name string }

func (e *duplicateErr) Error() string { return "duplicate secret: " + e.name }
func (e *duplicateErr) Unwrap() error { return secrets.ErrDuplicateSecret }

type notFoundErr struct{ id string }

func (e *notFoundErr) Error() string { return "not found: " + e.id }
func (e *notFoundErr) Unwrap() error { return secrets.ErrSecretNotFound }

func generateID() string {
	b := make([]byte, 16)
	// crypto/rand.Read is documented to never fail on Linux/macOS in
	// practice, but if entropy is somehow unavailable we'd produce
	// id collisions. Panic rather than silently degrading.
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generateID: crypto/rand.Read failed: %v", err))
	}
	return fmt.Sprintf("%x", b)
}

// bcryptPasswordUpdater implements handlers.PasswordHashUpdater using the DatabaseService.
type bcryptPasswordUpdater struct {
	db interfaces.DatabaseService
}

func (u *bcryptPasswordUpdater) UpdatePasswordHash(ctx context.Context, userID string, newPassword []byte) error {
	hash, err := bcrypt.GenerateFromPassword(newPassword, 12)
	if err != nil {
		return err
	}
	hashStr := string(hash)
	return u.db.UpdateUser(ctx, userID, types.UserUpdates{PasswordHash: &hashStr})
}

// dekMasterKey derives the DEK cache encryption key from the master secret.
// Uses HKDF with purpose-specific context so each derived key is independent.
func dekMasterKey() []byte {
	return deriveServerKey("dek-cache")
}

// deriveServerKey derives a 32-byte key from LLMSAFESPACE_MASTER_SECRET using HKDF.
// Each purpose string produces an independent key. Returns nil if master secret is unset.
func deriveServerKey(purpose string) []byte {
	masterHex := os.Getenv("LLMSAFESPACE_MASTER_SECRET")
	if masterHex == "" {
		// Fallback: check legacy env var
		masterHex = os.Getenv("LLMSAFESPACE_DEK_MASTER_KEY")
	}
	if len(masterHex) < 32 {
		return nil
	}
	master, err := hex.DecodeString(masterHex)
	if err != nil || len(master) < 16 {
		// If not valid hex, use raw bytes
		master = []byte(masterHex)
	}
	key, err := secrets.DeriveKEK(master, []byte("llmsafespace-server"), purpose)
	if err != nil {
		return nil
	}
	return key
}

// k8sWorkspaceGetterAdapter adapts the K8s client to the handlers.WorkspaceGetter interface.
type k8sWorkspaceGetterAdapter struct {
	client    *kubernetes.Client
	namespace string
}

func (a *k8sWorkspaceGetterAdapter) GetWorkspace(id string) (*v1.Workspace, error) {
	return a.client.LlmsafespaceV1().Workspaces(a.namespace).Get(id, metav1.GetOptions{})
}

// workspaceCRDGetter is the minimal interface needed by secretsPodIPResolver.
// Defined here (rather than reusing handlers.WorkspaceGetter) to keep the
// dependency direction one-way: app depends on handlers, not the other way.
type workspaceCRDGetter interface {
	GetWorkspace(id string) (*v1.Workspace, error)
}

// dbOwnerLookup is the minimal interface needed to verify workspace ownership
// for the secrets reload path. We require the database lookup (rather than
// trusting the CRD's spec.owner) because the API treats PostgreSQL as the
// authority for ownership at the API layer.
type dbOwnerLookup interface {
	GetWorkspace(ctx context.Context, workspaceID string) (*types.WorkspaceMetadata, error)
}

// secretsPodIPResolver resolves the pod IP for a workspace owned by a given
// user. Returns ("", nil) if the workspace is not owned by the caller, is
// not Active, has no PodIP yet, or the apiserver/DB is transiently
// unavailable. The handler treats every empty result as errNoRunningPod
// (409 Conflict) so the response shape is uniform across "you don't own
// this workspace" / "doesn't exist" / "DB is having a bad day" — this is
// deliberate: we do not want to leak workspace existence cross-user via
// status-code differences.
//
// Transient-failure errors (DB or apiserver blips) are still observable
// to operators because the resolver logs them at Warn before returning
// empty. Without that log a Postgres outage would produce silent 409s
// across the fleet with no signal in the API logs (Finding 2 in worklog
// 0094 follow-up audit).
//
// This adapter exists because handlers.SecretsHandler.SetPodIPResolver was
// never called from app.New — see Bug 1 in worklog 0085. Without it the
// reload-secrets endpoint returned 503 unconditionally and SetBindings'
// auto-push silently failed.
type secretsPodIPResolver struct {
	crd    workspaceCRDGetter
	db     dbOwnerLookup
	logger pkginterfaces.LoggerInterface
}

func newSecretsPodIPResolver(crd workspaceCRDGetter, db dbOwnerLookup, logger pkginterfaces.LoggerInterface) *secretsPodIPResolver {
	return &secretsPodIPResolver{crd: crd, db: db, logger: logger}
}

func (r *secretsPodIPResolver) GetWorkspacePodIP(ctx context.Context, userID, workspaceID string) (string, error) {
	if userID == "" || workspaceID == "" {
		return "", nil
	}

	// Ownership check first: a user must not be able to discover or push
	// to a pod they do not own. We treat both "not found", "owned by
	// someone else", and "DB blip" as a uniform empty result. The DB
	// blip is logged so operators can detect it.
	if r.db != nil {
		meta, err := r.db.GetWorkspace(ctx, workspaceID)
		if err != nil {
			if r.logger != nil {
				r.logger.Warn("secretsPodIPResolver: DB lookup failed; downgrading to no-running-pod",
					"workspaceID", workspaceID, "error", err.Error())
			}
			return "", nil
		}
		if meta == nil || meta.UserID != userID {
			return "", nil
		}
	}

	ws, err := r.crd.GetWorkspace(workspaceID)
	if err != nil {
		// Workspace CR missing or apiserver error — caller treats as
		// "no running pod"; do not surface raw K8s errors upstream.
		// Logged at Debug because CR-not-found is the common case for
		// freshly-created or terminating workspaces.
		if r.logger != nil {
			r.logger.Debug("secretsPodIPResolver: CRD lookup failed",
				"workspaceID", workspaceID, "error", err.Error())
		}
		return "", nil
	}
	if ws == nil || ws.Status.Phase != v1.WorkspacePhaseActive {
		return "", nil
	}
	return ws.Status.PodIP, nil
}
