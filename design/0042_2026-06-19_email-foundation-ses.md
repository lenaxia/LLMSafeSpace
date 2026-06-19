# Design 0042: Email Foundation via SES

**Date:** 2026-06-19
**Status:** Scoped — pending implementation
**Author:** Scope-out session
**Related:** Epic 49 (`design/stories/epic-49-email-foundation-ses/`)

---

## 1. Problem

LLMSafeSpaces needs outbound transactional email. Today there is **one** consumer (org invitations) and a working `EmailProvider` abstraction with an SES implementation — but the feature is half-wired and half-dead:

| Aspect | State | Evidence |
|--------|-------|----------|
| `EmailProvider` interface | ✅ Exists | `pkg/email/provider.go:10` — `Send(ctx, Message)` |
| SES implementation | ✅ Exists | `pkg/email/ses_provider.go` |
| Noop implementation | ✅ Exists | `pkg/email/noop_provider.go` |
| Config struct + env overrides | ✅ Exists | `config.go:118-126`, `:290-301` |
| Boot wiring (provider switch) | ✅ Exists | `app.go:558-581` |
| Helm `email:` values section | ❌ Missing | not in `values.yaml` |
| `configmap-api.yaml` email rendering | ❌ Missing | only server/db/redis/auth/ratelimit rendered |
| IRSA annotation docs | ⚠️ Supported but undocumented | `serviceaccount.yaml` renders annotations, no SES example |
| Password reset via email | ❌ Missing | `RecoverAccount` (`secrets.go:679`) is recovery-**key** based; its own comment says "In practice, this would be called after email verification" |
| Email verification on signup | ❌ Missing | no `email_verified` column or flow anywhere |
| Bounce handling | ❌ Dead code | `org_invitations.bounce_type`/`bounced_at` columns exist; `invitations.go:194` guards on them; nothing populates them (no SNS webhook) |
| `notifyOnSessionComplete` / `notifyOnWorkspaceReady` | ❌ Dead settings | `schema.go:100-101` define them; no consumer reads them |

**The net:** email plumbing exists but is env-var-only (inconsistent with every other config section, which is helm-first), has no production credential path documented, and powers only a fraction of the email the product needs.

---

## 2. Scope

### In scope

1. **SES operational readiness** — helm `email:` section, ConfigMap rendering, IRSA docs + NetworkPolicy/egress consideration, test-send endpoint.
2. **Helm-precedence settings model** — a new "Tier 1" concept: settings that are immutable when declared via helm, mutable in the admin UX otherwise. Generalised so future infra-locked settings (relay, billing) can reuse it.
3. **Password reset via email** — a token-based reset flow that does **not** require the recovery key; the recovery-key path stays as the offline fallback.
4. **Email verification on signup** — verify the signup email is deliverable/owned before enabling destructive account actions; gate-invitation-acceptance on verified email.

### Out of scope (noted as follow-ups)

- **SMTP provider** — same `EmailProvider` interface, additive. The SMTP password storage decision (encrypt in PG via master key) is recorded in §6.2 for the future epic. Tracked in Epic 49 §"Follow-ups".
- **SES bounce/complaint webhook** — would populate the existing dead `bounce_type`/`bounced_at` columns and make the `invitations.go:194` guard live. Tracked.
- **Notification email types** — wiring up the dead `notifyOnSessionComplete`/`notifyOnWorkspaceReady` user settings. Tracked.
- **Email-verification-gated org invitations at scale** — initial scope gates invitation *acceptance* on verified email only.

---

## 3. Assumptions — stated and validation plan

Per Rule 7. Each assumption has a validation mechanism. Status reflects work done during scoping.

| # | Assumption | Validation | Status |
|---|------------|------------|--------|
| A1 | API pod has unconstrained egress to `ses.<region>.amazonaws.com:443` | Read `charts/llmsafespaces/templates/workspace-network-policy.yaml` and confirm it applies `component=workspace` selectors only, not the API deployment | **Validated during scoping** — `networkPolicy` in `values.yaml:442` governs workspace pods; the API deployment has no egress NetworkPolicy. SES HTTPS egress works by default. |
| A2 | IRSA is the intended AWS credential path (no static keys) | Confirmed by `ses_provider.go` using `awsconfig.LoadDefaultConfig` with no static credentials, and the existing pattern of `externalSecret`/`masterSecret` going to K8s Secrets | **Validated** |
| A3 | SES sending requires a verified domain or identity in the target region | AWS SES operational requirement; documented in the epic README prerequisites | **Not validated — operator concern**; documented as a prerequisite in the story |
| A4 | The settings service is the right home for "is email helm-locked" signal | It owns all instance settings and is the read path for `GET /admin/settings`; the read-only UX needs this signal anyway | **Validated** — `settings.go:50-55` returns schema; we extend schema with a `ReadOnly`/`Locked` flag sourced from helm presence |
| A5 | Email-verification-gated invitation acceptance is safe because the user already authenticates with their account email | The existing `Accept` flow (`invitations.go:312-320`) already verifies the JWT user's email matches the invited email; verification adds "we also proved the mailbox is theirs" | **Validated** — the check at `invitations.go:317` is the existing defence; verification strengthens it |
| A6 | Password-reset tokens live in PostgreSQL with a short expiry and a hash (never the raw token) | Mirrors the invitation-token pattern at `invitations.go:406-419` (`crypto/rand` + `sha256` + base64, store hash only) | **Validated by precedent**; the invitation flow is the canonical pattern to copy |
| A7 | The `RecoverAccount` recovery-key flow must remain as an offline fallback | It is the only path when email is unconfigured; SES may be unreachable in air-gapped deployments | **Validated** — the NoopProvider path must keep working; reset-via-email is *additive*, not a replacement |
| A8 | Restart-after-config-change is acceptable for email settings | User decision during scoping; matches today's boot-time provider construction and the billing/relay config precedent | **Validated — user decision** |
| A9 | The provider **type** (ses/smtp/none) is helm-locked, not admin-mutable | User decision; provider type determines credential shape (IRSA vs username/password) and is an infra concern | **Validated — user decision** |
| A10 | SES SDK v2 is already a go.mod dependency | Check `go.sum` for `aws-sdk-go-v2/service/ses` | **Needs validation at implementation time** — `ses_provider.go` imports it, so it must be present, but run `go mod tidy` to confirm |

### Assumptions I will not make and need the user to confirm

None remaining for scoping. The forks were resolved in the question round.

---

## 4. End-to-end workflows

### 4.1 Operator enables SES (helm path)

```
operator: helm install ... --set email.enabled=true \
                            --set email.provider=ses \
                            --set email.sesRegion=us-east-1 \
                            --set email.fromAddress=noreply@example.com \
                            --set email.baseUrl=https://app.example.com \
                            --set serviceAccount.api.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::123:role/llmsafespaces-ses
  → chart renders email block into configmap-api.yaml
  → chart renders IRSA annotation onto the API ServiceAccount
  → API pod boots, app.go switches on provider=ses, constructs SESProvider
  → /readyz stays green
operator: hits POST /api/v1/admin/email/test { to: "ops@example.com" }
  → API sends a test email via the live SESProvider
  → 200 { sent: true } — or structured error if SES rejects (identity not verified, region wrong, IRSA missing)
```

### 4.2 Admin tunes email when NOT helm-configured

```
operator: helm install ... (no email block, or email.enabled=false)
  → API boots with provider=none (NoopProvider), no SES client
admin: opens Admin Settings → Email section
  → fields are EDITABLE (helm did not lock them)
admin: sets provider=ses, region, fromAddress, baseUrl via PUT /admin/settings/{key}
  → instance settings written to PostgreSQL
  → admin sees "restart required" notice (per A8)
operator: helm upgrade / kubectl rollout restart deployment/api
  → API reboots, reads instance settings, constructs SESProvider
```

### 4.3 Admin views email when helm-configured (read-only)

```
operator: helm install with email block set
  → configmap carries email block; on boot, app.go records which keys were "helm-provided"
admin: opens Admin Settings → Email section
  → fields show values but are DISABLED with a "Managed by Helm" badge
  → PUT /admin/settings/{email.*} returns 409 "setting is helm-managed"
```

### 4.4 User requests password reset

```
user: POST /api/v1/auth/password-reset/request { email: "alice@example.com" }
  → API looks up user by email
    → if not found: still return 202 (no enumeration; mirrors auth.go email-enumeration prevention)
    → if found: generate reset token (crypto/rand, sha256 hash), store hash + 15m expiry in PostgreSQL
    → send email with link {baseUrl}/reset-password?token=...
  → return 202 regardless
user: clicks link → frontend POST /api/v1/auth/password-reset/confirm { token, newPassword }
  → API verifies token hash, checks expiry, consumes token (single-use), rotates DEK via password, updates bcrypt hash
  → mirrors the existing RotateKeyWithPassword path so secrets stay decryptable
  → 200 { ok: true }
```

### 4.5 New user verifies email

```
user: POST /api/v1/auth/register { email, password, username }
  → user created with email_verified=false
  → generate verification token (crypto/rand, sha256 hash), store hash + 24h expiry
  → send verification email with link {baseUrl}/verify-email?token=...
user: clicks link → frontend POST /api/v1/auth/verify-email { token }
  → API verifies hash, checks expiry, sets email_verified=true, consumes token
  → 200 { ok: true }
```

**Open question for §6.3:** what is gated on `email_verified` in MVP? Options: (a) nothing — verification is advisory; (b) invitation acceptance; (c) all non-read actions. The story proposes (b) as the minimal meaningful gate.

---

## 5. Architecture

### 5.1 Right level of abstraction?

The existing `EmailProvider` interface is correctly sized — one method, two implementations, caller-shaped. **No change needed.** The work is operational (helm/config/IRSA) and product (new email types + new auth flows), not abstraction work.

Adding SMTP later is **open/closed**: a new `SMTPProvider` satisfies the existing interface; no consumer changes. This is the right shape — we are solving the operational + product gap, not reinventing the abstraction.

### 5.2 Helm-precedence model (the new concept)

Today settings have two tiers:

| Tier | Owner | Mutable in UX? |
|------|-------|----------------|
| 2 | Instance (admin) | Yes |
| 3 | Per-user | Yes |

This epic introduces **Tier 1** — settings that are infra-owned and immutable when present in helm:

| Tier | Owner | Mutable in UX? | Source |
|------|-------|----------------|--------|
| **1 (new)** | Helm/infra | **No, when helm-declared**; Yes when helm-omitted | Helm ConfigMap vs instance_settings |
| 2 | Instance (admin) | Yes | PostgreSQL |
| 3 | Per-user | Yes | PostgreSQL |

**Resolution rule (helm wins):**

```
effectiveValue(key) =
  if helmDeclared(key): helmValue          // read-only, ignores PG
  else: instanceSettings.Get(key)          // admin-mutable
```

**How the API knows a key is helm-declared:** the configmap renders a known set of keys into a separate block (e.g. `email.helmManaged: [provider, sesRegion, fromAddress, baseUrl]`). On boot, the settings service ingests this list and (a) serves those values, (b) marks them `readOnly: true` in the schema response, (c) rejects writes with 409.

**Why a separate signal, not "is the value non-empty":** an admin might legitimately set the same value via UX; the immutability must come from *provenance* (helm declared it), not *value shape*.

**Scope of the Tier-1 model:** implemented for email keys now. The mechanism (helm-managed-key list + readOnly schema flag) is general and can be reused for relay/billing/SSO later, but we do not retrofit those in this epic (avoid scope creep).

### 5.3 Interfaces (Go)

```
// Existing, unchanged — adding for reference
type EmailProvider interface {
    Send(ctx context.Context, msg Message) error
}

// New: a small service that owns email concerns beyond raw Send
type EmailService struct {
    provider email.EmailProvider
    config   EmailConfig     // resolved effective config (helm or PG)
    logger   LoggerInterface
}

// New: token store for password-reset + email-verification (one store, two token kinds)
type EmailTokenStore interface {
    CreateToken(ctx context.Context, t EmailToken) error
    GetByHash(ctx context.Context, hash string) (*EmailToken, error)
    Consume(ctx context.Context, id string) error  // single-use
}
type EmailToken struct {
    ID        string
    UserID    string
    Kind      EmailTokenKind  // "password_reset" | "email_verify"
    TokenHash string          // sha256(raw), never store raw
    ExpiresAt time.Time
    ConsumedAt *time.Time
}
```

**SOLID check:**
- **SRP:** `EmailProvider` sends; `EmailService` orchestrates (builds messages, tracks config); `EmailTokenStore` persists tokens. Three reasons to change, three types.
- **OCP:** new email types add new methods on `EmailService`; new providers implement `EmailProvider`. No existing type modified.
- **DIP:** handlers depend on `EmailService` interface, not the SES concrete. Tests inject NoopProvider.

### 5.4 Database

One new table, parameterised by `kind`:

```sql
CREATE TABLE email_tokens (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('password_reset', 'email_verify')),
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_email_tokens_user_kind ON email_tokens(user_id, kind);
CREATE INDEX idx_email_tokens_hash ON email_tokens(token_hash);
```

`users` gains `email_verified BOOLEAN NOT NULL DEFAULT false`. Backfill existing users to `true` (they predate verification; don't lock them out).

**Why one table, not two:** the two token kinds have identical shape and lifecycle. Splitting would duplicate columns and the consume/hash logic for no gain — that is over-engineering, not SOLID.

### 5.5 Secrets handling

- **SES credentials:** IRSA only. No password, no static key, nothing in PostgreSQL. Honours the "credentials exclusively in K8s Secrets" rule — the IRSA role binding IS the secret, managed by Kubernetes.
- **Token secrets:** reset/verify *tokens* are presented to the user via email and consumed once. We store the `sha256` hash only (never the raw token), exactly like `invitations.go:416-418`. A leaked DB row is useless without the email.
- **SMTP (future):** the SMTP password would be the one credential that lands in app config. The user's decision ("encrypt in PG via master key") is recorded in §6.2 for that future epic; it does not affect SES.

### 5.6 Configuration rendering

`configmap-api.yaml` gains:

```yaml
email:
  enabled: {{ .Values.email.enabled }}
  provider: {{ .Values.email.provider | quote }}    # "" | "ses"
  sesRegion: {{ .Values.email.sesRegion | quote }}
  fromAddress: {{ .Values.email.fromAddress | quote }}
  baseUrl: {{ .Values.email.baseUrl | quote }}
  # List of keys declared by helm — feeds the Tier-1 read-only signal
  helmManagedKeys:
    {{- range .Values.email._helmManagedKeys }}
    - {{ . | quote }}
    {{- end }}
```

`values.yaml` gains an `email:` section (default `enabled: false`, provider empty).

---

## 6. Decisions & open questions

### 6.1 Decided during scoping

| Decision | Rationale |
|----------|-----------|
| SES first, SMTP later | SES is already wired; IRSA eliminates the password-storage problem; SMTP is additive behind the same interface |
| Provider type helm-locked (Tier 1) | Credential shape (IRSA vs user/pass) is infra-level |
| Restart-required on email config change | Matches boot-time provider construction + billing/relay precedent |
| Password reset is additive to recovery key | Recovery key is the offline/air-gapped fallback; reset-via-email needs SES reachable |
| One `email_tokens` table for both kinds | Identical shape; splitting duplicates columns for no benefit |

### 6.2 Recorded for the future SMTP epic

The user chose "encrypt SMTP password in PostgreSQL using the master key" for when SMTP lands. This defers cleanly because:
- SES uses IRSA (no password) — zero conflict with the "credentials never in PG" rule
- The master-key/DEK crypto (`pkg/secrets/crypto.go`) is already production-validated for user secrets
- SMTPProvider would receive the decrypted password at construction; PG never holds plaintext

**Risk to flag in the SMTP epic:** storing an infra credential (encrypted) in PG blurs the "creds exclusively in K8s Secrets" rule even if cryptographically safe. That epic must explicitly reconcile the rule or propose an exception.

### 6.3 Open question — what does email_verified gate?

This is the single decision that blocks US-49.6 implementation. The full option analysis with tradeoffs lives in the epic README at **US-49.6 §6.2** (four options: advisory / invitation-acceptance / destructive-actions / all-non-read). The recommendation is **(b) invitation-acceptance only** because:

- It closes the one externally-tokened entry point (org membership via stolen invitation token) by composing with the existing JWT-email-match defence at `invitations.go:317`
- It is the minimal gate that delivers real security value without locking legitimate users out of their account
- Expanding the gate is a separate product decision best made once operators see real signup-conversion data post-launch

**Decision needed from user during implementation:** confirm (b), or expand scope. The decision also determines whether unverified users can log in (yes, regardless — see US-49.6 §6.3, blocking login creates a verify/reset deadlock whose only escape is the recovery key).

---

## 7. Quality attribute checklist

Per the user's framing (maintainable, scalable, robust, reliable, secure, performant, SOLID, idiomatic). Security controls are detailed per-story in the epic README's "Security controls & threat model" section.

- **Maintainable:** no new abstraction layers — reuses `EmailProvider`, extends `SettingDef` with one field, one new table. A junior can read the diff.
- **Scalable:** SESProvider holds one `*ses.Client` (connection-pooled by the SDK); `EmailService.Send` is stateless. No per-request allocation. The API is already horizontally scalable; email adds no shared state.
- **Robust:** send failures are logged + surfaced (test endpoint returns the SES error); password-reset request returns 202 even on user-not-found (no enumeration); token consume is single-use under the row lock; register fails-open on SES transient error with resend recovery.
- **Reliable:** deterministic token hashing; no flaky paths; SES SDK retries are the SDK's responsibility (configurable via `aws.Config`).
- **Secure:** token hashes only (sha256, 256-bit entropy), IRSA (no static creds), email-enumeration prevention on all request endpoints, single-use tokens with short TTL (15m reset / 24h verify), **session invalidation on password reset** (`RevokeAllUserSessions` reuses existing jti-revocation infra), **post-reset notification email** (OWASP-mandated victim alert), **interstitial pages** for all email-link flows (GET never consumes — scanner defence), invitation scanner-safety invariant locked in with regression test.
- **Performant:** email send is off the request hot path where possible (invitation emails already fire-and-forget; reset-request send is synchronous but bounded by SES SDK timeout). `RevokeAllUserSessions` runs only on password-reset confirm (rare), not on the hot path; the jti-tracking SET adds one `SADD` on login (off hot path).
- **SOLID:** see §5.3.
- **Idiomatic:** `(value, error)` returns, `errors.Is` error mapping (existing `handleSecretError` pattern), context propagation, strongly-typed `EmailTokenKind`.

### Security posture summary

| Dimension | Rating | Basis |
|-----------|--------|-------|
| Token confidentiality & integrity | Strong | 256-bit, sha256-hashed, single-use |
| Enumeration resistance | Strong | 202-always on all request endpoints |
| Credential storage (SES) | Strong | IRSA, no static keys |
| Credential storage (future SMTP) | Deferred | Recorded; will need rule reconciliation |
| Session hygiene on credential change | Strong | `RevokeAllUserSessions` on password reset (Gap 1 fixed) |
| Post-incident alerting | Strong | Post-reset notification email (Gap 2 fixed) |
| Email-link scanner robustness | Strong | Interstitial pages for all flows; invitation invariant locked in (Gap 3 fixed) |
| Email deliverability | Strong | DKIM/SPF/DMARC documented as operator prerequisites |
| XSS in email bodies | Medium | Centralised in `EmailService` (US-49.7); existing `html.EscapeString` in invitations |

**Residual risk:** advanced sandboxed scanners that render pages and click buttons are not fully mitigated by interstitial pages alone. Full mitigation requires two-step codes (out of scope MVP). The interstitial is the industry-standard first defence and matches the existing invitation pattern. Revisit if production reports scanner-consumed tokens.

---

## 8. What this epic explicitly does NOT do

- Does not build SMTP (noted, future, behind the same interface)
- Does not wire up SES bounce/complaint (the dead `bounce_type` columns stay dead; flagged)
- Does not wire up the dead `notifyOnSessionComplete`/`notifyOnWorkspaceReady` settings (flagged)
- Does not retrofit Tier-1 onto existing relay/billing/SSO config (mechanism is general; scope is email only)
- Does not change the recovery-key flow (stays as the offline fallback)

---

## 9. Validation gate before implementation

Per Rule 11, this scope must survive adversarial review before stories start:

1. **Is the helm-precedence model right-sized?** Tier-1 is one new field on `SettingDef` + a boot-time list. Two implementations of provenance detection would be over-engineered; zero (always mutable) violates the requirement. ✅ right-sized.
2. **Is one email_tokens table correct?** Two kinds, identical columns, identical lifecycle. Splitting duplicates. ✅ correct.
3. **Is SES-first the right call vs SMTP-first?** SES is wired, IRSA eliminates the password-storage fork, SMTP is additive. The only argument for SMTP-first is self-hosted/air-gapped operators — but those run NoopProvider today and are not blocked. ✅ SES-first is correct.
4. **Does password-reset via email weaken security vs recovery-key?** Reset tokens are single-use, hashed, 15m expiry, email-enumeration-safe. Recovery key stays as offline fallback. Reset is *easier* for users, *equivalent* in blast radius (both grant account takeover if the channel is compromised — email inbox vs recovery-key leak). ✅ acceptable, documented tradeoff.
