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
| A1 | `NewAuditedProvider` has zero production call sites | `rg "NewAuditedProvider\("` across `*.go` returns 3 hits: the definition (`audited_provider.go:50`) + 2 test-file constructions (`audited_provider_test.go:56,163`). Confirmed. |
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
# 3 hits: 1 definition + 2 test constructions. Confirms G50.

# Confirm rotate-kek CLI absent (A4)
ls cmd/rotate-kek/ 2>&1
# ls: cannot access 'cmd/rotate-kek/': No such file or directory

# Confirm file-mount chart default (A2) — read api-deployment.yaml:67-130
# Confirm fail-closed loader (A3) — read secrets_adapters.go:553-563

# Pre-commit gates (doc-only → skips Go/helm/migration gates):
# repolint + worklog-sentinel + gitleaks will run on commit.
```

---

## Next Steps

- PR this change. CI for doc-only paths runs `lint` (repolint) only.
- If the reviewer accepts the G50 framing, the next Epic 50 work item that closes it is US-50.2 (unify the crypto layers under `RootKeyProvider`), in progress on `feat/epic-50-us50.7-domain-separation`.
- When US-50.2 lands and wires `AuditedProvider` into all 9 decrypt sites, flip G50 from 🔴 to 🟢 with file:line evidence and update §10 counts.

---

## Files Modified

- `design/stories/epic-17-security-review/THREAT-MODEL.md` — v2.2: §2 asset, §4.1 nodes [2.3]–[2.5], §5 gaps G48–G50, §6 STRIDE Database row, §8 A8, §10 recounted summary, §11 revision entry.
- `worklogs/NNNN_2026-06-22_threat-model-epic-50-sync.md` — this worklog (sentinel; post-merge bot assigns the real number).
