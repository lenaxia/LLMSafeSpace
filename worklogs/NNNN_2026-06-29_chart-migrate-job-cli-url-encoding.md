# Worklog: Migrate Job CLI args — shell wrapper + URL encoding (#455)

**Date:** 2026-06-29
**Session:** Fix the migration Job connection-string bug that blocks helm upgrades (libpq KV form rejected by `migrate/migrate:v4.17.1` CLI with `error: no scheme`).
**Status:** Complete

---

## Objective

Implement issue #455 (option 1, recommended). The chart's migrate Job passed `-database` in libpq KV form (`host=... password=...`). KV is a golang-migrate **library-only** feature; the standalone CLI in the Docker image requires `driver://url` and dies with `error: no scheme`. This was latent since PR #437 (#424) and only surfaced when PR #451 added the first real post-baseline migration — the pre-upgrade hook failed, helm rolled back, Flux retried, loop. Mitigated in prod by `psql` + `migrations.enabled: false`.

The fix must produce a CLI-valid `postgres://` URL while keeping the password out of the rendered YAML (secrets stay in the Secret, read at runtime).

---

## Assumptions (stated + validated)

1. **The `migrate/migrate:v4.17.1` CLI requires `driver://url` for `-database`.** → Validated: issue #455 quotes `migrate -help` and a live repro returning `error: no scheme`.
2. **`migrate` is on PATH in the image** (so `exec migrate` resolves after overriding the entrypoint). → Validated against the tagged Dockerfile (v4.17.1): `COPY .../migrate.linux-386 /usr/local/bin/migrate` + `ln -s .../migrate /migrate`; alpine PATH includes `/usr/local/bin`.
3. **The image's busybox `od`/`tr`/`sed` produce the same bytes as GNU coreutils for the encoding pipeline.** → Validated by downloading busybox v1.35.0 (musl static) and running the exact pipeline. Byte-identical for the full reserved-char set (`/ ? # @ : % + = &`, space, tab). See "Validation evidence".
4. **`printf '%s' "$VAR"` preserves the argument literally** (no escape interpretation). → Validated via the round-trip below (a literal `%` and tab survived).
5. **`postgres://` userinfo percent-decodes back to the original password.** → Validated via Go `url.Parse` in the integration test and python `urlparse` against the busybox-produced URL.

Uncovered by automated tests (prod-cluster validation only): the migrate binary connecting to a live Postgres with the encoded URL. Consistent with how the codebase gates cluster-dependent e2e.

---

## Work Completed

- **`templates/migration-job.yaml`**: replaced bare `args:` (libpq KV) with `command: ["/bin/sh", "-ec"]` + a single script arg. The script percent-encodes every byte of both `DB_USER` and `DB_PASSWORD` at runtime (`printf '%s' "$X" | od -An -tx1 | tr -d ' \n' | sed 's/../%&/g'`), then `exec migrate` with `postgres://${enc_usr}:${enc_pwd}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=${DB_SSLMODE}`. Encoding every byte is wasteful but unconditionally correct for any password/user content. Connection params read from env vars at runtime (not rendered from the Secret). Comment block records both #424 and #455 history.
- **`chart_migration_job_test.go`** (rewritten): header documents both #424 and #455.
  - `TestMigrationJob_UsesShellWrapperWithEncodedURL` — command is `/bin/sh -ec`, script builds a `postgres://` URL, `exec migrate`, all six env vars referenced.
  - `TestMigrationJob_PasswordURLEncodedNotRaw` — the encoding pipeline is present; raw `DB_PASSWORD`/`$(DB_PASSWORD)` never reaches the `postgres://` line (only `enc_pwd` does). Inverts the old assertion that mandated the KV form.
  - `TestMigrationJob_PasswordFromSecret` — kept (DB_PASSWORD from secretKeyRef).
  - `TestMigrationJob_ScriptProducesValidURLOnReservedCharPassword` — integration-level: renders the chart, extracts the exact script bytes, executes it against a `migrate` shim with a password containing every URL-reserved char plus space/tab, then asserts the migrate binary received a valid `postgres://` URL whose decoded userinfo matches the original byte-for-byte.

### Adversarial self-review (Rule 11)

The biggest unvalidated assumption was #3 (busybox vs GNU). Validated via the busybox round-trip below — byte-identical. Other reviewed failure modes (all documented as non-issues): `printf '%s'` literal semantics (proved), `exec migrate` PATH (Dockerfile-confirmed), `readOnlyRootFilesystem: true` (pipes only, no writes), migrate error-log leaking the URL (pre-existing inherent exposure; the new form actually improves `ps` exposure since the password is percent-encoded in argv vs the old raw `password=...`), `pipefail` not set (mid-pipeline failure surfaces as a migrate error, non-silent). Zero real findings.

---

## Key Decisions

- **Option 1** (shell wrapper + URL-encode) per the issue recommendation — smallest diff, no new image, no new code path.
- **Encode BOTH user and password, every byte.** The issue's proposed fix encoded only the password; encoding the user too is strictly more robust (an `@`/`:` in the username would break userinfo parsing). "Typically safe" identifiers are the unvalidated assumption Rule 7 warns against.
- **`exec migrate`, not `exec /migrate`.** Both resolve (Dockerfile-confirmed); `migrate` (PATH) is test-friendly (the shim test finds it via PATH).
- **Shim-based integration test rather than spawning the real migrate image.** Spawning `migrate/migrate:v4.17.1` + Postgres needs Docker/testcontainers, which the chart suite does not use. The shim proves script→URL→parse end-to-end; migrate-vs-Postgres is the deploy.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 180s ./charts/llmsafespaces/                                  # PASS (12.0s, full suite)
go test -timeout 120s -run 'TestMigrationJob' -v ./charts/llmsafespaces/        # 4/4 PASS
helm lint ./charts/llmsafespaces                                               # 0 failed
gofmt -l <changed files>     # clean
go vet ./charts/llmsafespaces/   # clean
```

### Validation evidence (busybox round-trip, outside the Go suite)

```
input : P@ss/w0rd?+#%=& a:b<TAB>q
busybox: %50%40%73%73%2f%77%30%72%64%3f%2b%23%25%3d%26%20%61%3a%62%09%71
gnu    : %50%40%73%73%2f%77%30%72%64%3f%2b%23%25%3d%26%20%61%3a%62%09%71   (identical)
```

Full chart script under busybox `sh` → `postgres://%75%40...:%50%40...@h:5432/db?sslmode=require`; python `urlparse` decodes userinfo back to the original reserved-char password byte-for-byte.

---

## Next Steps

- Address automated-reviewer findings on this PR; iterate to APPROVE; squash-merge.
- After merge: re-enable `migrations.enabled: true` in the Flux HelmRelease and confirm the migrate Job applies migrations 2–4 cleanly against the live DB.
- Companion PR: #456 (Flux reconcileStrategy docs) — #456's stale chart is what masked #455.

---

## Files Modified

- `charts/llmsafespaces/templates/migration-job.yaml`
- `charts/llmsafespaces/chart_migration_job_test.go`
- `worklogs/NNNN_2026-06-29_chart-migrate-job-cli-url-encoding.md` (this worklog)
