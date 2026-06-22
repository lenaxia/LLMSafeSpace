# Worklog: Epic 54 (Org-Scoped Login) — Scaffold + Reviewer Corrections

**Date:** 2026-06-22
**Session:** Scope and scaffold Epic 54 (Org-Scoped Login — email discovery + subdomain routing) after discovering the BYO-email login gap; open PR; apply reviewer corrections.
**Status:** Complete (PR open, corrections applied, awaiting re-review)

---

## Objective

Multi-tenant deployments using an external IdP (Authelia, Authentik, Keycloak) where members BYO their email addresses have no working login path today: `LoginPage.tsx:42` only renders the "Sign in with {orgName}" button when the typed email's domain matches a verified `claimed_domain`, and SSO-provisioned users have a random unusable bcrypt hash so the password form is permanently blocked for them. Scope an epic that fixes this without leaking the customer list (no org picker) and without magic links (user-rejected). Open the PR and iterate through the automated reviewer.

---

## Work Completed

### Scoping conversation (pre-PR)

Walked through the problem and rejected options:
- Org picker — leaks customer list to unauthenticated users.
- Per-org direct URLs (`/auth/sso/<slug>/start`) — phishing-y, doesn't scale with org count.
- Magic links — user explicitly rejected ("absolutely no magic link shit").
- Email → org membership lookup returning a list — same leak as org picker.

Converged on the **Slack pattern**: email → silent redirect to org subdomain, response-masked so found/not-found are indistinguishable. Deferred passkeys (zero existing primitives) and multi-org membership (keep 1:1 for now). Decided to spike wildcard subdomain routing first since that's the load-bearing unknown.

### Scaffold

Created `design/stories/epic-54-org-scoped-login/`:
- `README.md` — spike (S54-0) + 3 stories (US-54.1 lookup endpoint, US-54.2 root discovery page, US-54.3 Helm wildcard ingress). ~5 days after spike.
- `DECISIONS.md` — D54-1 (no magic links), D54-2 (single `redirectUrl`), D54-3 (keep 1:1), D54-4 (spike first), D54-5 (default-off chart flag).
- Added row to `design/stories/README.md` V2.2 (In Planning) table.

### PR #341

Opened `docs(epic-54): scaffold org-scoped login epic` against `main`. CI green on: Lint, Gitleaks, govulncheck, Trivy, Build Frontend (amd64/arm64), Secrets Integration, Prepare build metadata. The "review" workflow posted an automated AI review.

---

## Key Decisions

- **D54-1 (no magic links):** Email is for invitations + notifications only, never auth. Passkeys replace the password path when built.
- **D54-2 (single `redirectUrl`):** Public surface never reveals org membership. Forward-compatible with multi-org (resolver change only, API contract stable).
- **D54-3 (keep 1:1):** User: "We may add multi-org support in the future, but will not do so immediately." The single-`redirectUrl` contract holds under multi-org — only the resolver body changes.
- **D54-4 (spike first):** Wildcard subdomain routing is the unknown. 1-day spike gates the epic; org picker is the documented fallback if infra blocks.
- **D54-5 (default-off):** `orgSubdomainRouting.enabled=false` — backward-compatible with single-host deploys.

### Reviewer-driven corrections (applied this session)

The automated reviewer caught six issues; all six verified correct against source and fixed:

1. **CRITICAL — D54-3 DB constraint claim was inverted.** My initial doc claimed the 1:1 invariant was "app-layer only, no DB unique index." Wrong. `migration 000036_single_org_enforcement.up.sql:12-13` creates `CREATE UNIQUE INDEX idx_org_memberships_single_user ON org_memberships(user_id)`. I had only inspected migration 000029 when researching; the constraint was added later by 000036. Corrected: README Current State table, D54-3 Context, and the multi-org migration path now include `DROP INDEX idx_org_memberships_single_user` as the first step.

2. **Wrong primitive cited for the resolver.** I wrote `ListOrgsForUser` at `pg_org_store.go:804`. Actually: `ListOrgsForUser` is at `:243` and returns `[]*types.OrgResponse` (full org objects — unnecessarily heavy for a 1:1 lookup). Line 801 is `GetUserOrgID(ctx, userID) (string, error)` — a single-row `SELECT org_id FROM org_memberships WHERE user_id = $1`. That's the right primitive. Corrected all references (README resolver pseudocode, file reference table, DECISIONS D54-3 resolver examples).

3. **Timing-pad framing overstated "the precedent."** I proposed a `time.Sleep` gated by a config flag and claimed this "follows `password_reset.go:119`." It doesn't — `password_reset.go:119` returns 202 uniformly with **no sleep**. The reviewer correctly flagged that a static sleep adds a config flag + test-disable mechanism without addressing found-branch variance under DB load. Simplified D54-2 to match the precedent exactly: uniform response, no sleep, no config flag. Noted that a pad can be added later if implementation-time measurement shows divergence.

4. **Line drift in `orgs.go`.** Wrote `405-418` for the single-org enforcement; actual range is `409-420` (lines 404-407 are the "already a member" check, a different concern).

5. **Line drift in `config.go`.** Copied `128-138` from README-LLM.md §14; the OIDC struct is at `:134-139` with the comment starting at `:129`.

6. **Missing worklog.** README-LLM.md:612 explicitly lists "Completing a design document" as a mandatory worklog trigger. I had followed Epic 43's D5 ("no worklogs for design phase") but that was a per-epic exception, not platform-wide. This entry corrects the omission.

Also resolved an internal inconsistency: README.md US-54.1 said "not-found redirect deferred to implementation" while DECISIONS.md D54-2 said "default decided." Both now say decided: root login with `?lookup=not_found`.

---

## Blockers

None. PR is open with corrections applied; awaiting re-review.

---

## Tests Run

No code or tests in this PR (design-only). Pre-commit hooks passed on the corrected commit (repolint; gofmt/goimports/golangci-lint/helm-render/migration-safety/gitleaks skipped — no relevant file types staged). CI ran on the original push; re-review will re-trigger CI on the corrections push.

Manual verification of reviewer claims (before applying fixes):
- `api/migrations/000036_single_org_enforcement.up.sql` — confirmed UNIQUE INDEX on `org_memberships(user_id)`.
- `pg_org_store.go:243` — confirmed `ListOrgsForUser` returns `[]*types.OrgResponse`.
- `pg_org_store.go:801` — confirmed `GetUserOrgID` returns `(string, error)` via single-row SELECT.
- `password_reset.go:119-129` — confirmed no `time.Sleep`; uniform 202 on both branches.
- `orgs.go:409-420` — confirmed single-org enforcement range.
- `config.go:129-139` — confirmed OIDC struct location.

---

## Next Steps

1. Push corrections, reply to the reviewer comment acknowledging the catches, request re-review (`/review` or `/ai`).
2. On APPROVE: hold for explicit `/merge` (design PR — never auto-merges per the AI commands footer).
3. When implementation kicks off: start with spike S54-0 (wildcard subdomain routing on staging). 1-day time-box. If it fails, replan toward org picker fallback (~2 days frontend-only).
4. After spike passes: US-54.1 (backend lookup, ~4h), US-54.2 (frontend root discovery, ~4h), US-54.3 (Helm chart, ~6h). No migrations in this epic.

---

## Files Modified

- `design/stories/epic-54-org-scoped-login/README.md` — created (scaffold), then corrected (DB constraint, file refs, timing pad, line drifts, not-found redirect consistency)
- `design/stories/epic-54-org-scoped-login/DECISIONS.md` — created (scaffold), then corrected (D54-2 timing pad, D54-3 DB constraint + resolver primitive)
- `design/stories/README.md` — added Epic 54 row to V2.2 (In Planning) table
- `worklogs/0497_2026-06-22_epic-54-scaffold-reviewer-corrections.md` — this entry
