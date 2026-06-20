# testharness

Single source of Postgres + Redis wiring for the API service's integration
tests.

## What it provides

`testharness.New(t)` returns a `*Harness` with:

| Method | Returns | Purpose |
|---|---|---|
| `Pool()` | `*pgxpool.Pool` | pgx connection pool (for pgx-based stores) |
| `SQLDB()` | `*sql.DB` | database/sql handle (for stores like `PgEmailTokenStore`) |
| `Redis()` | `*redis.Client` | client backed by an isolated miniredis |
| `Miniredis()` | `*miniredis.Miniredis` | direct access for TTL / key-scan assertions |
| `MigrateUp()` / `MigrateDown()` | `error` | apply/revert the embedded migration set (idempotent) |
| `Reset()` | `error` | `TRUNCATE` every user table (`RESTART IDENTITY CASCADE`); never touches `schema_migrations` |
| `NewContext()` | `context.Context` | context with a 30s test deadline (cancel wired to `t.Cleanup`) |
| `Logger()` / `Logs()` | `*zap.Logger` / `*ObservedLogs` | in-memory log capture for "no ERROR was logged" assertions |
| `Seed(table, row)` | `string` | thin typed convenience for "insert a user, get its id" |
| `DSN()` / `ID()` | `string` | resolved connection string / per-instance unique marker |

Every handle is closed via `t.Cleanup`, so tests never manage teardown.

## When to use the harness vs. mocks

| The test needs… | Use |
|---|---|
| One function, no I/O | A plain unit test — no harness |
| Real Postgres semantics (constraints, arrays, `ON CONFLICT`, FK cascade) | The harness |
| Real Redis semantics (TTL, pipelining, Lua) | The harness (`Redis()` / `Miniredis()`) |
| Multiple services wired against a real DB | The harness |
| A full HTTP request through router → service → DB | The harness + `httptest` |
| K8s client interactions | `controller-runtime/client/fake` — **not** this harness |

Rule of thumb (epic design principle P5): a mock that reimplements a database
badly is the most common source of "tests pass, prod breaks" in this codebase.
If the behaviour under test depends on Postgres/Redis being correct, use the
harness; if it depends only on call sequencing, a small testify mock is fine.

## Isolation model

The harness connects to a **single, shared, externally-provisioned** test
Postgres given by `TEST_DATABASE_URL` (default
`postgres://postgres:testpass@localhost:5433/llmsafespaces_test?sslmode=disable`).
If Postgres is unreachable, `New(t)` calls `t.Skip` — so `go test ./...` stays
green on a machine without Docker/Postgres, and CI (where
`TEST_DATABASE_URL` is provisioned) runs the suite for real.

- **Migrations** are applied on construction and are idempotent: in CI the DB
  is already migrated by the `migrate` step, so `MigrateUp` is a no-op
  (`ErrNoChange`); on a fresh local DB the embedded migration set brings the
  schema current.
- **Per-test isolation** follows the project convention: use a unique marker
  per test (see `ID()`) so parallel tests do not collide. `Reset()` is provided
  for **non-parallel** tests that need a clean slate; it truncates user tables
  and never touches `schema_migrations`.
- **Redis** is a per-harness miniredis instance, so Redis state is always
  isolated automatically.

This matches how the codebase's integration tests already work (unique IDs on a
shared DB). It deliberately does **not** provide per-instance database
isolation (the testcontainers path); that was evaluated and rejected to avoid
introducing a Docker dependency — see the worklog for US-52.6.

## Minimal example

```go
//go:build integration

package myfeature_test

import (
	"context"
	"testing"

	"github.com/lenaxia/llmsafespaces/api/internal/testharness"
)

func TestMyStore_RoundTrip(t *testing.T) {
	h := testharness.New(t)
	store := NewMyStore(h.Pool())
	ctx := h.NewContext()

	id := "my-" + h.ID()
	if _, err := h.Pool().Exec(ctx,
		`INSERT INTO users (id, username, email, password_hash, active, role)
		 VALUES ($1, $2, $3, 'h', true, 'user')`,
		id, "u_"+id, id+"@test"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool().Exec(context.Background(), "DELETE FROM users WHERE id = $1", id)
	})

	// … exercise the store …
}
```

## Scope and limitations

- **Only `api/`-rooted tests can import this package.** It lives under
  `api/internal/`, so Go's internal-visibility rule forbids imports from
  outside `api/`. In particular, `pkg/secrets` integration tests **cannot**
  use it; they retain their own `getTestPool` helper. Cross-layer
  consolidation of those tests is deferred (would require relocating the
  harness core to `pkg/testharness` with an injectable migration source).
- **No K8s.** This harness is for Postgres + Redis. K8s integration uses
  envtest (US-52.1) and the kind e2e runner (US-52.7).
- **`MigrateDown()` is destructive.** On the shared test DB it destroys the
  schema for every other test; use it only against a throwaway database.

## Verification

```bash
# Pure-helper unit tests (run everywhere, no DB required):
go test -timeout 60s -race ./api/internal/testharness/...

# Contract tests (require TEST_DATABASE_URL; skip otherwise):
go test -tags integration -timeout 120s -race ./api/internal/testharness/...
```
