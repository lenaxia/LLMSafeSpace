# Worklog: Threat model sync — Epic 50 master-KEK hardening landings

**Date:** 2026-06-22
**Session:** Bring `THREAT-MODEL.md` (epic-17) in line with the Epic 50 (master KEK hardening) commits that landed on main, and file the tracking issue for the one component that shipped but is not yet wired into production.
**Status:** Complete

---

## Objective

After `git pull` brought in a batch of Epic 50 commits (US-50.1 file mount, US-50.3 key-version columns, US-50.4 multi-key provider, US-50.6 rotation-aware write path, US-50.12 decrypt audit), the epic-17 threat model was stale: it still asserted the master KEK was delivered as an env var (now fixed by default), recorded no rotation primitives (now partially shipped), and claimed decrypt operations were unaudited without naming the shipped-but-unwired `AuditedProvider`. The threat model is the authoritative security reference; keeping it out of sync is a Rule 5 / Rule 7 violation (assumption drift).

Two deliverables: (1) update `THREAT-MODEL.md` to v2.2 with verified file:line evidence, and (2) open a tracking issue for the gap that is not yet wired (G50).

---

## Work Completed

### `design/stories/epic-17-security-review/THREAT-MODEL.md` → v2.2

Every change cites evidence verified against the live code (not the design doc's claims).

- **§2 Assets** — added the server master KEK as an explicit Critical asset, documenting the file-mount default (`/etc/llmsafespaces/master-secret`, mode 0440) and the deprecated env-var opt-in.
- **§4.1 Attack tree (credential theft)** — added nodes [2.3]–[2.5] under "From API server":
  - [2.3] `/proc/1/environ` exposure via env-var delivery — now 🟢 Fixed (US-50.1), with the file-loader fail-closed behaviour and deprecation Warn cited.
  - [2.4] in-memory KEK exposure on process compromise — documented as residual; KMS/Vault (H3) deferred by design per epic-50 README §Deferred.
  - [2.5] KEK blast radius — now bounded by the rotation primitives (US-50.3/.4/.6); operational CLI (US-50.5) pending.
- **§5 Gaps** — added three:
  - **G48** master KEK env-var delivery — 🟢 Fixed (US-50.1).
  - **G49** no operational KEK rotation — 🔴 Open (provider/columns/write-path shipped; `rotate-kek` CLI pending).
  - **G50** decrypt operations not audited — 🔴 Open. `AuditedProvider` shipped (`pkg/secrets/audited_provider.go:42-73`) but **has zero call sites outside its own tests**; Layer 2 callers still call `DecryptSecret(deriveServerKey(label), ct)` directly. Wiring awaits US-50.2 unification.
- **§6 STRIDE** — Database row updated: credential rows now carry `key_version` (US-50.3); authorized-decrypt exfiltration undetectable until G50 is wired.
- **§8 Assumption A8** — split: JWT signing-key rotation still Refuted; master-KEK rotation now Partial.
- **§10 Summary** — recounted the gap table. The v2.1 summary reported 18 Fixed / 22 Open; a row-by-row recount showed 16 Fixed / 24 Open (the prior counts were already stale before this session). Folded the recount into the new totals: 17 Fixed / 26 Open / 7 Accepted / 50 Total (reconciles exactly).
- **§11 Revision history** — added v2.2 entry.

### Tracking issue

Opened [issue #366](https://github.com/lenaxia/LLMSafeSpaces/issues/366) — "G50 / US-50.12: AuditedProvider shipped but not wired into production decrypt paths". Documents the evidence (`NewAuditedProvider` has no non-test call sites), the impact (authorized-decrypt exfiltration undetectable; rotation is "calendar theatre" without it per Epic 50 D7), and the US-50.2 dependency. Tagged `documentation` (no `security`/`epic-50` label exists in the repo).

---

## Key Decisions

1. **G50 is Open, not Fixed.** The `AuditedProvider` component shipped with full test coverage, but per README-LLM.md Rule 0 ("Definition of done" requires demonstrated integration via passing e2e/integration tests; "it works in isolation" does not satisfy this), an unwired component provides no production coverage. The threat model must reflect the *as-deployed* state, not the as-coded state. This is the single most important judgement call in this session.

2. **Did not invent a `security` or `epic-50` label.** The repo's label set is inconsistent (epic-24/29/40 exist; epic-42/43/50/51 do not). Creating labels unilaterally on a docs PR would be scope creep. Used `documentation` (an existing label that fits a tracking item) and noted the choice in the issue body.

3. **Recounted the whole gap table rather than just appending G48–G50.** The v2.1 totals were already wrong (18/22 reported vs 16/24 actual) before this session. Appending three rows to a broken total would propagate the error. The honest fix is a full recount, documented in §10 with the prior-vs-actual delta so the correction is auditable.

4. **Did not modify the Epic 50 design doc or any code.** The threat model describes reality; reality is changed by the Epic 50 stories, not by this session. Touching `epic-50-master-kek-hardening/README.md` would conflate description with prescription.

---

## Assumptions

| # | Assumption | Validation |
|---|---|---|
| A1 | `NewAuditedProvider` is never called anywhere | `rg "NewAuditedProvider\("` across `*.go` returns a single hit — the constructor definition at `audited_provider.go:50`. Zero call sites in production *or* test: the two test references (`audited_provider_test.go:56,163`) are `&AuditedProvider{}` struct literals that bypass the constructor (so it is itself untested). Confirmed. *(Corrected from an earlier draft that misread an OR-ed grep — `NewAuditedProvider\(\|\.audit\b` — as constructor calls; the `.audit` arm matched the struct-literal field, not `NewAuditedProvider`. Caught by the AI reviewer.)* |
| A2 | US-50.1 file mount is the chart default | `charts/llmsafespaces/templates/api-deployment.yaml:67` gates on `default "file" .Values.masterSecret.deliveryMethod`; `chart_master_secret_test.go:121-200` asserts the volume + `LLMSAFESPACES_MASTER_SECRET_FILE` are present in the default render. Confirmed. |
| A3 | The file loader fails closed on a short/mis-mounted active file | `secrets_adapters.go:553-563` (`activeMasterSecret`): if the active file material is `< 32` bytes, returns nil rather than falling back to an earlier key. Confirmed. |
| A4 | US-50.5 (`rotate-kek` CLI) has not shipped | `cmd/rotate-kek/` does not exist (`ls cmd/`). The Epic 50 README lists US-50.5 as Phase 4 work. Confirmed. |
| A5 | The v2.1 summary counts (18 Fixed / 22 Open) were stale | Row-by-row recount of the §5 table in the pre-edit file: Fixed rows are G2,G5,G8,G11,G12,G15,G16,G17,G18,G19,G20,G22,G24,G26,G27,G31 = 16. Confirmed stale. |

---

## Adversarial self-review

**Phase 1 — Findings:**

1. *Does marking G50 as "not wired" contradict the US-50.12 worklog which says "Status: Complete"?* Re-read worklog 0513: it describes the component as built and tested, and correctly scopes its claim to `pkg/secrets`. It does not claim production wiring. The threat model's stricter bar (as-deployed state) is the correct one for a security reference. Not a contradiction.

2. *Is the [2.5] "🟢 Partial" marker inconsistent with the status legend (which has only Open/Accepted/Fixed)?* The legend defines gap statuses, not attack-tree node markers. [2.3] uses 🟢 for a fixed node; [2.5] uses 🟢 Partial to indicate the rotation *primitives* are shipped while the *operational CLI* is pending. The qualifier "Partial" makes the distinction explicit. Acceptable — but if a reviewer flags it, demote to plain text.

3. *Are the cited line numbers stable?* `secrets_adapters.go:525-571`, `root_key.go:62-118`, `api-deployment.yaml:112-130` all verified against the current tree. They will drift on the next edit to those files — but every other gap row in this threat model has the same property, so this is consistent with the document's convention.

4. *Did the recount miss anything?* Recomputed: Fixed=16 (listed in A5), Accepted=7 (G1,G3,G7,G10,G14,G23,G32), Open=24 (the remaining G-numbers from 1–47) + 3 new (G48 Fixed, G49 Open, G50 Open). New totals: Fixed=16+1(G48)=17, Open=24+2(G49,G50)=26, Accepted=7. 17+26+7=50. Reconciles.

**Phase 2 — All findings validated as false alarms or accepted edge cases with rationale above.**

**Phase 3 — No remediation needed.**

---

## Blockers

None.

---

## Tests Run

Doc-only change; no Go/frontend tests apply. Verifications run:

```bash
# Confirm AuditedProvider is unwired (A1)
rg "NewAuditedProvider\(" --glob '*.go'
# 1 hit: the constructor definition at audited_provider.go:50.
# (The two test refs are &AuditedProvider{} struct literals — see A1.)
# => NewAuditedProvider is never called anywhere; constructor is untested. Confirms G50.

# Confirm rotate-kek CLI absent (A4)
ls cmd/rotate-kek/ 2>&1
# ls: cannot access 'cmd/rotate-kek/': No such file or directory

# Confirm file-mount chart default (A2) — read api-deployment.yaml:67-130
# Confirm fail-closed loader (A3) — read secrets_adapters.go:553-563

# Pre-commit gates (doc-only → skips Go/helm/migration gates):
# repolint + worklog-sentinel + gitleaks will run on commit.
```

---

## Iteration (post-creation)

After the initial commit + PR #367, CI surfaced a pre-existing red on main that blocked the docs PR. The iteration:

1. **Diagnosed the red.** main had been failing the `Secrets Integration` job since `547ff337` (US-50.6): three go-sqlmock tests in `api/internal/services/database/database_test.go` had stale column-count expectations (US-50.6 added a `key_version` column to two queries; the mocks weren't updated). Confirmed pre-existing — the docs PR is two `.md` files and cannot affect Go/Postgres tests.

2. **Opened a separate fix PR (#370)** rather than folding a Go test fix into the docs PR (single-responsibility, Rule 4). Verified locally, AI-reviewed with APPROVE. Added a follow-up commit hardening the touched tests (`require` before indexing; `ExpectationsWereMet()` alignment) per reviewer notes.

3. **#370 was superseded.** While iterating, US-50.7 (PR #365, `09c0d17c`) merged to main and independently fixed the same three mocks (it branched off the same broken main). main went green. Closed #370 as redundant; left the two hardening suggestions as optional follow-ups in the close comment.

4. **Corrected two self-introduced inaccuracies in G50** (one self-caught, one caught by the AI reviewer — both Rule 7 failures).
   - **(a) US-50.2/US-50.7 conflation** (self-caught on post-merge re-verification): the first draft wrote "Wiring depends on US-50.2 ... in progress on `feat/epic-50-us50.7-domain-separation`", conflating US-50.2 (unify crypto layers / remove `AdminKeyDeriver`, **not merged**) with US-50.7 (domain-separate the api_keys provider, **merged**). Verified against live main: `AdminKeyDeriver` still exists (`pkg/secrets/credential_store.go:81`). Corrected the G50 text to distinguish the two stories and cite `AdminKeyDeriver`'s presence as the concrete blocker. Folded US-50.7 into attack-tree node [2.5].
   - **(b) Misread grep evidence** (caught by the AI reviewer, REQUEST CHANGES): the first draft claimed `rg "NewAuditedProvider\("` returns "3 hits: the definition + 2 test-file constructions". That was a misread of an OR-ed grep (`NewAuditedProvider\(\|\.audit\b`) — the `.audit` arm matched the struct-literal field in the test's `&AuditedProvider{inner: inner, audit: fake}`, not a `NewAuditedProvider(` call. The accurate result: `NewAuditedProvider\(` matches exactly 1 line (the constructor definition); the tests bypass the constructor via struct literal, so `NewAuditedProvider` is **never called anywhere** (production *or* test) and the constructor is itself untested. Corrected in G50 text, worklog A1, and the PR body. The G50 *conclusion* (Open) was unaffected — if anything the constructor being untested strengthens it.

   Both are the failure mode Rule 7 exists to catch: an assumption stated as "Confirmed" without actually validating the tool output. (b) in particular shows why external review matters — the adversarial self-review (Phase 1–3) did not catch it; the AI reviewer did.

---

## Next Steps

- PR this change. CI for doc-only paths runs `lint` (repolint) only.
- If the reviewer accepts the G50 framing, the next Epic 50 work item that closes it is US-50.2 (unify the crypto layers under `RootKeyProvider`), in progress on `feat/epic-50-us50.7-domain-separation`.
- When US-50.2 lands and wires `AuditedProvider` into all 9 decrypt sites, flip G50 from 🔴 to 🟢 with file:line evidence and update §10 counts.

---

## Files Modified

- `design/stories/epic-17-security-review/THREAT-MODEL.md` — v2.2: §2 asset, §4.1 nodes [2.3]–[2.5], §5 gaps G48–G50, §6 STRIDE Database row, §8 A8, §10 recounted summary, §11 revision entry.
- `worklogs/0518_2026-06-22_threat-model-epic-50-sync.md` — this worklog (sentinel; post-merge bot assigns the real number).
