# Worklog: Epic 50 — Phase 1 Quick Wins (US-50.8, US-50.9, US-50.10, US-50.11)

**Date:** 2026-06-20
**Session:** Implement Phase 1 of Epic 50 "Master KEK Hardening" — the four no-dependency quick-win stories, designed to ship as one PR.
**Status:** Complete (implementation + validation); commit/push/PR pending explicit user instruction.

---

## Objective

Begin Epic 50 implementation at the entry point mandated by the epic's execution
strategy and worklog 0437's next steps: **Phase 1 — Quick wins (US-50.8, US-50.9,
US-50.10, US-50.11)**. These four stories have no dependencies, are individually
small (≤0.25d), and the design specifies they ship as one PR. They harden the
master-KEK surface without touching the high-blast-radius unification (US-50.2,
deferred to Phase 2).

---

## Work Completed

### US-50.8 — `static` deprecation warning fires on the Helm-empty default

- **Problem (A4, confirmed):** `newRootKeyProvider` (`api/internal/app/secrets_adapters.go`)
  already matched `case "static", "":`, but the warning guard was
  `if provider == "static"` — so the Helm default (`""`) silently used the
  dev-only static path with no warning.
- **Fix:** replaced the guard with `if !cfg.Security.SkipMasterKeyWarning`
  inside the `case "static", ""` branch, so it fires for BOTH empty and explicit
  `"static"` unless suppressed.
- **Config:** added `Security.SkipMasterKeyWarning bool` (`config.go`) +
  `LLMSAFESPACES_SECURITY_SKIPMASTERKEYWARNING=true` env binding, matching the
  sibling `LLMSAFESPACES_SECURITY_*` pattern.
- **TDD:** red → `TestNewRootKeyProvider_EmptyDefault_LogsWarning` failed first;
  after the fix all four tests pass (`EmptyDefault`, `ExplicitStatic`,
  `SkipWarning_Suppresses`, `Sealed_NoWarning`) using `logger.NewObserved()`.
- **Scope note:** per the design's US-50.8 file list, no Helm rendering was added
  — `rootKeyProvider` is itself not surfaced in the chart today, so the warning's
  acceptance ("fresh Helm install logs the warning") is satisfied by the empty
  default alone, and suppression works via the env var.

### US-50.9 — Sealed provider threat-model documentation

- **New `pkg/secrets/README.md`** (79 lines): provider implementations table,
  attacker-class × provider threat matrix, provider selection guide, planned
  external (Vault/OpenBao Transit) provider note, and the V0/V1 sealed-key file
  format section.
- **`SealedKeyProvider` doc comment** (`pkg/secrets/root_key.go`) explicitly
  states what it does and does NOT defend against (in-memory exposure under
  process compromise).
- **Static warning text** now points operators to `pkg/secrets/README.md`.
- Docs-only by design — no tests; acceptance is review.

### US-50.10 — `seal-key` no longer prints the root key by default

- **Problem (A11, confirmed):** `cmd/seal-key/main.go:59` unconditionally printed
  the generated root key to stderr.
- **Fix:** removed that line; added a `-print-key` flag that opts in to emit
  `WARNING: …` + hex key to **stdout** (pipeable, not stderr). Default emits no
  key bytes anywhere.
- **TDD:** `TestSealKey_Default_NoKeyInOutput` (no 64-hex run in stdout/stderr),
  `TestSealKey_PrintKey_OutputsToStdout`, `TestSealKey_PrintKey_NotOnStderr`.
  All pass; no script/doc depended on the old stderr line (grep confirmed only
  `main.go:59` referenced it).

### US-50.11 — HKDF domain separation + `LSKP-S` sealed-key format versioning

- **Problem (A12/A22, confirmed):** `SealRootKey`/`unsealKey` derived the sealed
  KEK via `DeriveKEKFromPassword(passphrase, salt)` with no HKDF info; the
  constant `sealedKeyInfoStr` (`pkg/secrets/root_key.go:13`) was dead code.
- **`DeriveSealedKEK(password, salt, info)`** (`pkg/secrets/crypto.go`): mixes
  `info` into Argon2id's salt by deriving a 32-byte sub-salt via the existing
  `DeriveKEKFromKey(salt, nil, info)` (HKDF-SHA256), then Argon2id with the
  unchanged memory-hard parameters. Argon2id is retained (NOT the weaker
  HKDF-only `DeriveKEKFromPasswordV0`); only the salt input changes.
- **V1 format:** `SealRootKey` now writes `magic "LSKP-S"` ‖ `salt` ‖ `ct`.
  `unsealKey` routes by magic prefix: V1 (`unsealKeyV1`, info-mixed KEK) vs
  legacy V0 (`unsealKeyV0`, plain Argon2id). `sealedKeyInfoStr` is now consumed.
- **Backward compat:** legacy V0 files (salt ‖ ct, no magic) still unseal;
  proven by `TestSealedKeyProvider_UnsealLegacyV0Format`. The 1/2^48 chance of a
  random V0 salt starting with `LSKP-S` is documented; it surfaces as a clean
  GCM auth-tag failure, never silent corruption.
- **TDD:** `TestDeriveSealedKEK_*` (5: produces-32B, different-info→different,
  distinct-from-plain-argon, different-passwords, rejects-wrong-salt) +
  `TestSealedKeyProvider_RoundTrip_V1Format` + `TestSealedKeyProvider_UnsealLegacyV0Format`;
  updated `TestSealRootKey_DeterministicFormat` for the V1 layout (≥98 bytes +
  magic prefix). Cross-package consumer
  `api/internal/services/auth/TestCreateAPIKey_WithSealedKeyProvider` passes.

### Validation (independent skeptical validator, Rule 11)

- A fresh-context validator independently verified assumptions A4/A11/A12/A22,
  every acceptance criterion, the full SealRootKey/unsealKey/NewSealedKeyProvider
  call-site census, crypto soundness, and US-50.10 leakage. **Verdict: PASS on
  all four stories** — no BLOCKER/HIGH findings. Eight candidate issues were
  investigated and dismissed as false alarms with evidence (V0 magic collision,
  HKDF-on-salt weakening, Truncated/Corrupted tests, cross-package auth test,
  `-print-key` leakage, goroutine safety).
- Four LOW/cosmetic notes (non-blocking): one test renamed vs the design's
  literal name (`TestDeriveSealedKEK_*` — the design's name referenced a
  function that does not exist), the no-leak regex lacks word boundaries (sound
  as-is), the warning is unit-tested (consistent with package style), and the
  new env binding has no `config_test.go` coverage (matches the pre-existing
  pattern for sibling bindings).

---

## Key Decisions

1. **Start with Phase 1.** The design's execution strategy and worklog 0437 both
   designate the four quick-win stories as the entry point. US-50.2 (unification)
   is the high-blast-radius critical path and is deliberately left for Phase 2.
2. **US-50.11 KEK design = HKDF sub-salt + Argon2id.** Chosen over the weaker
   HKDF-only `DeriveKEKFromPasswordV0` (which drops Argon2id's memory-hardness for
   the low-entropy passphrase). Reuses the existing `DeriveKEKFromKey` helper —
   no new HKDF boilerplate. Different `info` reliably yields independent KEKs
   (proven by `TestDeriveSealedKEK_DifferentInfoProducesDifferentKeys`).
3. **V1 format uses a 6-byte magic (`LSKP-S`) with no separate version byte.**
   The magic IS the version indicator (presence == V1, absence == V0). A version
   byte would anticipate a V2 that does not exist (Rule 4: not over-engineered).
   This is the one justified use of a magic prefix (sealed-key files are
   standalone artifacts detached from any DB `key_version` column — per the
   design's Out-of-Scope rationale).
4. **US-50.8 stays within the design's file list** (code + config field + env
   binding). No Helm rendering — faithful to the ask; `rootKeyProvider` itself is
   not in the chart today, so adding only `skipMasterKeyWarning` would be
   inconsistent scope creep.
5. **Test naming:** the design's `TestDeriveKEKFromPasswordSealed_*` referenced a
   function that does not exist; the implemented function is `DeriveSealedKEK`,
   so the tests are named `TestDeriveSealedKEK_*` (spirit preserved).

---

## Blockers

- **Sandbox instability (process, not code):** twice during the session an
  external process switched branches and ran `git reset --hard HEAD`, destroying
  the uncommitted Phase-1 working-tree changes (the untracked `pkg/secrets/README.md`
  survived both events). Mitigation: authoritative final copies of all 10 files
  were kept in `/tmp/opencode/final/` and restored each time; `origin/main` also
  advanced by 3 commits during the session (epic43 SSO domain verification,
  epic-49 email-token tests, repolint renumber), and the branch was fast-forwarded
  to the latest tip (`ac87b7bb`) so the diff contains only Epic-50 Phase-1 files.
- **Not committed:** per the agent's no-unrequested-commit policy, changes are
  left uncommitted on `feat/epic-50-master-kek-quick-wins` pending explicit user
  instruction to commit/push/open PR. (This is also what left the work exposed to
  the reset events — committing is the durable protection.)

---

## Tests Run

All on `feat/epic-50-master-kek-quick-wins` at `origin/main` (`ac87b7bb`), with
`GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=*`:

```
gofmt -l <9 changed .go files>                                  # clean (lists nothing)
go build ./...                                                  # BUILD OK
go vet ./...                                                    # VET OK
golangci-lint run ./pkg/secrets/... ./cmd/seal-key/... \
  ./api/internal/app/... ./api/internal/config/...              # 0 issues (errcheck fix applied)
go test -timeout 150s -race -count=1 \
  ./pkg/secrets/ ./cmd/seal-key/ ./api/internal/config/ \
  ./api/internal/app/                                           # ok (all four)
go test -timeout 60s -race ./api/internal/services/auth/ \
  -run 'TestCreateAPIKey_WithSealedKeyProvider'                 # ok (cross-package sealed consumer)
```

Note: `make lint` could not run directly (`golangci-lint` not preinstalled;
installed v2.12.2 to `/tmp/opencode/gobin`). The full `./api/internal/services/auth`
suite is too slow under `-race` in this sandbox (bcrypt); only the sealed-key
consumer relevant to US-50.11 was run and passes.

---

## Next Steps

1. **User reviews and authorizes commit + push + PR** for this branch
   (`feat/epic-50-master-quick-wins`). Recommend squash-merge after the automated
   reviewer approves.
2. **Committing immediately** is recommended to durably protect the work against
   the sandbox reset behavior observed this session.
3. **Phase 2 (critical path):** US-50.1 (master KEK as a file mount) and
   US-50.2 (unify the two crypto layers under `RootKeyProvider`). US-50.2 is
   high-blast-radius and must land with its full failure-mode test matrix
   (D6). Both can start in parallel.
4. **Re-validate** the touched packages after any rebase before merge.

---

## Files Modified

- `api/internal/config/config.go` — added `Security.SkipMasterKeyWarning` field + env binding.
- `api/internal/app/secrets_adapters.go` — static warning fires on empty+static unless suppressed; text points to `pkg/secrets/README.md`.
- `api/internal/app/secrets_adapters_test.go` — US-50.8 tests (4).
- `pkg/secrets/crypto.go` — added `DeriveSealedKEK(password, salt, info)`.
- `pkg/secrets/crypto_test.go` — `TestDeriveSealedKEK_*` (5).
- `pkg/secrets/root_key.go` — `sealedMagicV1`; `SealedKeyProvider` doc comment; `SealRootKey` writes V1; `unsealKey` routes V1/V0; `sealedKeyInfoStr` consumed.
- `pkg/secrets/root_key_test.go` — V1 round-trip + legacy-V0 unseal tests; updated `TestSealRootKey_DeterministicFormat`; `bytes` import.
- `cmd/seal-key/main.go` — removed default key print; added `-print-key` (stdout, explicit errcheck discard).
- `cmd/seal-key/main_test.go` — US-50.10 tests (3); updated size assertions to the V1 floor (98).
- `pkg/secrets/README.md` (new) — threat model + provider selection guide + file-format section.
- `worklogs/0446_2026-06-20_epic-50-phase1-quick-wins.md` (new) — this worklog.
