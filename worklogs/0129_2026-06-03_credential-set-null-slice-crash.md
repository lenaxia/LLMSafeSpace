# Worklog: credential-set null-slice JSON crash

**Date:** 2026-06-03
**Reporter:** Operator hit the bug when adding a new LLM provider in the Settings → Credential Sets UI.
**Crash:** `Cannot read properties of null (reading 'length')` at `Array.map` in the bundled JS.

---

## Bug

Frontend stack trace pointed at the `.map` callback in
`AdminCredentialsTab.tsx`:

```tsx
{sets.map((cs) => (
  ...
  Providers: {cs.providers.join(", ") || "none"} · Models: {cs.modelAllowlist.length || "all"}
  ...
))}
```

Both `cs.providers` and `cs.modelAllowlist` are typed as `string[]`,
but the runtime value was `null`. The TypeScript type lied because the
backend serialized `null` for these fields when the underlying Go
`[]string` was nil.

The user-visible repro was: open Settings → Credential Sets → "Add" →
fill in name + provider + apiKey → submit. POST `/admin/credentials`
succeeded; the new `CredentialSet` object surfaced into the list state;
React re-rendered; `.length` on `null` blew up the whole tab.

---

## Root cause

Two paths in `pkg/credentials/service.go` produced nil slices that
encoded to JSON `null`:

### Path 1 — Create response (the user's exact path)

```go
modelAllowlist := req.ModelAllowlist
if modelAllowlist == nil {
    modelAllowlist = []string{}        // defaulted FOR THE DB WRITE
}
id, err := s.store.CreateCredentialSet(ctx, ..., modelAllowlist, ...)
...
return &CredentialSet{
    ...
    ModelAllowlist: req.ModelAllowlist, // <-- but the RESPONSE used the request's nil
    ...
}
```

A previous fix (commit `b7548de`,
`fix(credentials): default nil modelAllowlist to empty slice on create`)
defaulted only the DB write, missing the response object. The frontend
form does not surface a model-allowlist input, so `req.ModelAllowlist`
is nil for every UI-driven create — and the response always sent
`modelAllowlist: null`.

### Path 2 — Get/List path (rowToCredentialSet)

```go
var providers []string
if plaintext, err := Decrypt(...); err == nil {
    if config, err := UnmarshalProviders(plaintext); err == nil {
        for name := range config {
            providers = append(providers, name)   // happy path: providers is non-nil
        }
    }
}
// failure path: providers stays nil

return &CredentialSet{
    ...
    Providers:      providers,            // <-- nil if decrypt OR unmarshal failed
    ModelAllowlist: row.ModelAllowlist,   // <-- nil if pq driver returned nil
}
```

Same wire-format crash on Get / List for any row whose decrypt failed
mid-rotation, or any row written before commit `b7548de` whose
`model_allowlist` column was NULL.

`encoding/json` serializes `[]string(nil)` as `null`, never `[]`. The
frontend's `cs.providers.join` and `cs.modelAllowlist.length` then
throw — the React error overlay replaces the whole settings page.

---

## Fix

### Backend (`pkg/credentials/service.go`)

1. **Create**: bind the response's `ModelAllowlist` to the
   already-defaulted local variable, not `req.ModelAllowlist`.
2. **rowToCredentialSet**: initialize `providers := []string{}` (not
   `var providers []string`); apply the same nil-→-`[]` normalization to
   `row.ModelAllowlist` before assigning.

Both fields now have the invariant: **the response struct's slices
are always non-nil**. JSON encoding therefore always emits `[]`, never
`null`. The contract is now honest with the TypeScript type.

### CI parity (`pkg/credentials/service_test.go`)

Added 4 regression-guard tests:

| Test | Asserts |
|---|---|
| `TestCredService_Create_ResponseHasNonNilSlices` | Create with no `ModelAllowlist` produces `cs.ModelAllowlist != nil` AND the JSON body does not contain `"modelAllowlist":null`. **Direct guard for the user's repro.** |
| `TestCredService_Get_ResponseHasNonNilSlices` | Get ditto. |
| `TestCredService_List_ResponseHasNonNilSlices` | List ditto — this is the exact path the user's UI iterated. |
| `TestCredService_RowToCredentialSet_DecryptFailure_ProvidersNotNil` | When decrypt fails, Providers is `[]` not `nil`. Pins the deepest failure path. |

Pre-fix, the first and fourth tests fail with the literal user-error
JSON in the assertion message. Post-fix, all four pass.

### Frontend defense-in-depth (`AdminCredentialsTab.tsx`)

Even with the backend fixed, the frontend now treats null/undefined
arrays as `[]`:

```tsx
Providers: {(cs.providers ?? []).join(", ") || "none"} ·
Models: {(cs.modelAllowlist ?? []).length || "all"}
```

Rationale: TypeScript types are a compile-time lie until the wire
format is verified. A future regression in the backend (e.g., a new
field added without the same nil-default discipline) would otherwise
re-crash the whole settings tab. The `?? []` is cheap and the cost of
NOT having it is the React error overlay replacing the page.

### Frontend test (`AdminCredentialsTab.test.tsx`)

New test `tolerates null arrays (provider response regression guard)`
mocks the API to return `providers: null, modelAllowlist: null` —
the EXACT shape that crashed the user — and asserts the component
renders the placeholders ("Providers: none", "Models: all") without
throwing. Pre-fix this test reproduces the user's error
(`Cannot read properties of null (reading 'join')` in the test
output); post-fix it passes.

---

## Verification

| Command | Result |
|---|---|
| `go test -count=1 ./pkg/credentials/` | PASS (33 tests, 4 new) |
| `golangci-lint run ./pkg/credentials/` | 0 issues |
| `npx vitest run src/components/settings/AdminCredentialsTab.test.tsx` | 9 passed |
| `npx vitest run` (full frontend suite) | 591 passed |

---

## Related work

- `b7548de` — earlier partial fix (DB write only). Closed half the
  Create path; this commit closes the rest.
- The bug surfaced after the LLM-credential-flow work in worklogs
  0125–0128. None of those introduced the null-slice issue — it
  pre-existed in `service.go`. Discovery happened only because the
  operator finally exercised the admin credentials UI.

---

## Files Modified

- `pkg/credentials/service.go`
- `pkg/credentials/service_test.go`
- `frontend/src/components/settings/AdminCredentialsTab.tsx`
- `frontend/src/components/settings/AdminCredentialsTab.test.tsx`
- `worklogs/0129_2026-06-03_credential-set-null-slice-crash.md` (this file)
