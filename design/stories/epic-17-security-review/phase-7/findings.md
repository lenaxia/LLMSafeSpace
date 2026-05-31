# Phase 7 — App Logic + Frontend XSS — Findings

**Status:** Complete
**Cluster:** `admin@home-kubernetes`, image `sha-eb5c33e`
**Frontend:** `https://safespace.thekao.cloud` (Traefik ingress)
**Harness:** [`harness/run-phase7.py`](./harness/run-phase7.py)
**Worklog:** [`worklogs/0092_*-epic17-phase-7-app-logic-frontend.md`](../../../../worklogs/)

## Summary

| Result | Count |
|---|---|
| PASS | 12 |
| FAIL | 2 |
| INCONCLUSIVE | 1 |

## New gaps surfaced

| ID | Severity | Title |
|---|---|---|
| **G31** | medium | Frontend ingress lacks CSP and X-Frame-Options (RT-7.13) |
| **G32** | low | No per-user workspace quota (RT-7.1) — accepted in single-tenant deployments |

## Per-test results

| ID | Result | Sev | Title |
|---|---|---|---|
| RT-7.1 | FAIL | low | No quota: 8 workspace creations succeeded back-to-back |
| RT-7.2 | PASS | info | Rapid suspend/resume produced no 5xx |
| RT-7.3 | PASS | info | 5 concurrent secret PUTs converged |
| RT-7.4 | PASS | info | Workspace transfer not implemented |
| RT-7.5 | PASS | info | No injection detector (single-tenant accepted) |
| RT-7.6 | PASS | info | Activity tracking is controller-only |
| RT-7.7 | PASS | info | Per-user workspace name scoping works |
| RT-7.8 | PASS | info | Delete-with-active-stream produced clean responses |
| RT-7.9 | PASS | info | MessagePart uses `rehypeSanitize`; no innerHTML |
| RT-7.10 | PASS | info | Code blocks via React text-mode (auto-escape) |
| RT-7.11 | PASS | info | Tool input via `JSON.stringify` (safe) |
| RT-7.12 | PASS | info | Diff viewer not in use OR upstream-safe |
| RT-7.13 | **FAIL** | medium | Frontend ingress lacks CSP/X-Frame-Options |
| RT-7.14 | PASS | info | JWT in HttpOnly+Secure cookie; no localStorage refs |
| RT-7.15 | INCONCLUSIVE | info | DEK race on workspace delete (combined w/ RT-4.12) |

## Frontend XSS — what we tested vs what we couldn't

**Tested (PASS):**
- Static analysis of `frontend/src/components/chat/MessagePart.tsx`:
  - `rehype-sanitize` plugin attached to `ReactMarkdown` for both assistant and user messages.
  - No `dangerouslySetInnerHTML` anywhere in the chat path.
  - No raw `innerHTML` writes.
  - Tool input rendered via `JSON.stringify()` then placed in a `<pre>` (React auto-escapes children).
- JWT storage: HttpOnly+Secure cookie, no localStorage / sessionStorage refs in `frontend/src/api/`.

**Not tested live (INCONCLUSIVE — emitted for follow-up):**
- 11-payload XSS bypass corpus written to `evidence/RT-7.9-xss-corpus.json`. To exercise this against the deployed app properly, you need either:
  - A Vitest/Jest unit test that mounts each payload through ReactMarkdown + rehypeSanitize and asserts the rendered DOM has no `<script>`, no `on*=` attribute, no `javascript:` URI.
  - A Playwright test that drives the live app, sends each payload through the chat, and watches for unexpected `alert()` or DOM mutations.

The corpus is recorded so the follow-up test has zero ambiguity about scope.

## RT-7.13 — Frontend CSP/XFO gap

**Reproduction:**
```
$ curl -s -I https://safespace.thekao.cloud
... (no Content-Security-Policy, no X-Frame-Options)
```

**Comparison:** The API DOES set strong headers:
```
Content-Security-Policy: default-src 'self'; connect-src 'self' wss:; ... frame-ancestors 'none'
X-Frame-Options: DENY
```
The API headers are added by Gin middleware. The frontend is served by Traefik ingress to a static-served bundle (or via Frontend deployment). Either path should add the same headers.

**Impact:**
- Without `frame-ancestors 'none'` or `X-Frame-Options: DENY`, the frontend can be iframed by any other origin → clickjacking attack surface.
- Without CSP, a hypothetical XSS that bypasses rehype-sanitize gets full `eval()` and inline-script power.

**Fix options:**
- Add HTTP headers to the Traefik IngressRoute (or whatever ingress controller is in use):
  ```yaml
  spec:
    rules:
    - http:
        paths:
        - path: /
          pathType: Prefix
          backend: ...
    # plus a Middleware:
  ---
  apiVersion: traefik.io/v1alpha1
  kind: Middleware
  metadata: { name: security-headers }
  spec:
    headers:
      contentSecurityPolicy: "default-src 'self'; ..."
      frameDeny: true
      ...
  ```
- Or include them in `frontend/.../public/_headers` if static-served.
- Or add to the LLMSafeSpace frontend deployment if it goes via a Go server.

## Pre-existing gaps re-confirmed

- **G18** logout doesn't revoke (RT-7.14 still PASS because cookies are HttpOnly+Secure; the dormant revocation gap is a different layer — see Phase 4 RT-4.13).

## Cleanup

`phase7-{alice,bob}@pentest.local` users + 8 quota-test workspaces deleted. No residue.

## Files

- `design/stories/epic-17-security-review/phase-7/harness/run-phase7.py` (~700 lines)
- `design/stories/epic-17-security-review/phase-7/findings.md`
- `design/stories/epic-17-security-review/phase-7/evidence/RT-7.{1..15}.json`
- `design/stories/epic-17-security-review/phase-7/evidence/RT-7.9-xss-corpus.json` (11 payloads)
- `worklogs/0092_2026-05-30_epic17-phase-7-app-logic-frontend.md` (this file)
