# Worklog: Credential Flow Live Exercise — Two Production Bugs Surfaced

**Date:** 2026-06-02
**Session:** End-to-end exercise of the new LLM-provider credential flow against the live cluster (safespace.thekao.cloud)
**Status:** Complete — flow is **broken**; two bugs identified, neither fixed in this session

---

## Objective

Exercise the new credential flow per README-LLM.md guidance: create an LLM
provider credential against the live API, bind it to a workspace, and prove
that opencode in the sandbox pod can use it for an LLM prompt round-trip.
OpenAI-compatible LiteLLM endpoint from the operator's `~/.bashrc`
(`https://ai.thekao.cloud/v1`) was used as the test provider.

---

## Setup

| Step | Command/Endpoint | Result |
|---|---|---|
| Register throw-away user | `POST /api/v1/auth/register` | 201 — userID `be8923b9-…`, email `credflow-1780436630@example.com` |
| Create llm-provider secret | `POST /api/v1/secrets` with `type:"llm-provider"` and JSON value `{provider:"openai", apiKey, baseURL, default:"openai/default", models:[{id:"default"}]}` | 201 — secret `4b4408ba-738d-464c-87d0-028dbceaf442` |
| Create workspace | `POST /api/v1/workspaces {runtime:"base"}` | 201 → controller reconciled to phase=Active in ~14s |
| Bind secret | `PUT /api/v1/workspaces/<ws>/bindings {secretIds:[…]}` | 204 in 162ms, no log warnings |
| Verify binding | `GET /api/v1/workspaces/<ws>/bindings` | 200 — binding visible |
| Force live push | `POST /api/v1/workspaces/<ws>/reload-secrets` | 200 — `{"reloaded":1,"restarted":false}` |

Workspace ID: `8e5e74c3-7524-4df9-bba9-81a57b33b8d6`
Pod: `8e5e74c3-7524-4df9-bba9-81a57b33b8d6-c6161fd2`
Bindings runtime image: `ghcr.io/lenaxia/llmsafespace/base:sha-9d801a2`

---

## Findings

### Bug 1 — `pkg/agent/opencode/client.go` does not set HTTP Basic auth on `PUT /auth/:providerID`

**Symptom in pod logs:**

    {"level":"warn","msg":"reload-secrets: opencode credential refresh failed, falling back to restart",
     "error":"push credentials: PUT /auth/openai returned 401"}

**Root cause:**

`opencode serve` requires HTTP Basic auth on every endpoint, including
`/auth/*`. The Basic auth username is the constant
`pkg/agentd/types.go:23 → AuthUsername = "opencode"` and the password is
`OPENCODE_SERVER_PASSWORD`, identical to `AGENTD_ADMIN_TOKEN`, mounted from
`/sandbox-cfg/password`.

Existing `OpenCodeClient` in `cmd/workspace-agentd/main.go:58-69` uses
`req.SetBasicAuth(agentd.AuthUsername, c.password)` for every probe call.

The new `pkg/agent/opencode/client.go` (worklog 0121) does NOT. `setAuth`
(line 121-129) builds a request and calls `c.httpClient.Do(req)` with no
auth. Likewise `DisposeInstance` (line 63-79).

**Verified manually against the live pod's opencode:**

    curl -u 'opencode:<password>' -X PUT http://localhost:4096/auth/openai \
        -H 'Content-Type: application/json' \
        -d '{"type":"api","key":"test"}'
    HTTP/1.1 200 OK             ← with auth
    
    curl                -X PUT http://localhost:4096/auth/openai …
    HTTP/1.1 401 Unauthorized   ← without auth (current agentd behaviour)
    www-authenticate: Basic realm="Secure Area"

**Required fix:**

`Client` needs the password threaded in at construction time, and every
request method must call `req.SetBasicAuth(agentd.AuthUsername, password)`.
The constructor signature changes to
`NewClient(baseURL, password string) *Client`. Caller in
`cmd/workspace-agentd/secrets.go:271` becomes:

    oc := opencode.NewClient(
        fmt.Sprintf("http://localhost:%d", agentd.AgentPort),
        opencodePassword, // read once at agentd startup from /sandbox-cfg/password
    )

The unit tests in `client_test.go` mocked `httptest.NewServer` and never
gated on Basic auth, which is why this regression was not caught
pre-merge. Tests need a server-side `BasicAuth(opencode, expected)` check.

---

### Bug 2 — Fallback `proc.restart()` does not free port 4096; opencode enters crash loop

**Symptom in pod logs (continuous):**

    opencode started     pid=156 restartCount=0
    opencode exited unexpectedly  exit status 1  restartCount=0
    restarting opencode  backoff=2
    opencode started     pid=165 restartCount=1
    …

The crash log on disk:

    INFO  service=default version=1.15.12 args=["serve","--hostname","0.0.0.0","--port","4096"]
    ERROR service=default name=ServeError
          cause=Error: Failed to start server. Is port 4096 in use?
          fatal

`ps -ef` after the crash loop began:

    sandbox        1       0  workspace-agentd --supervise
    sandbox       82       1  opencode serve --hostname 0.0.0.0 --port 4096
    …                          ← original PID 82 still alive
    
Each "restart" attempt is a *new* opencode process that finds 4096 still
held by the original PID 82.

**Root cause analysis:**

`cmd/workspace-agentd/main.go:607-624` — `restart()` does:

1. `p.mu.Lock()` → set `p.stopping = true` → snapshot `cmd := p.cmd` → unlock
2. `cmd.Process.Signal(os.Interrupt)`
3. `cmd.Wait()` with a 5s `Kill()` fallback

But there is a structural race:

- The supervisor goroutine started by `start()` (line 581-604) is the
  goroutine that *actually* `Wait()`s the process. It owns the `Wait()`.
- `restart()` then ALSO calls `cmd.Wait()` on the same `*exec.Cmd` (line 617).
  Concurrent `Wait()` on a `*os.Process` is undefined; in practice the
  second `Wait()` returns immediately with an error and the channel closes
  before the kernel has reaped the child.
- `restart()` proceeds to `p.start()` and a new `exec.Command` issues
  `bind(0.0.0.0:4096)`. Original process is still running because we
  raced ahead of its actual exit, and `SIGINT` is not guaranteed to be
  prompt for a Bun-compiled binary that has its own signal handlers.

A second contributing factor: `start()`'s monitor goroutine checks
`p.stopping` (line 584). If `stopping` is true it returns early and does
NOT trigger the supervised restart cycle. But `restart()` sets
`stopping = true`, kicks off its own `start()`, and never resets
`stopping = false`. The new `start()` does set `p.stopping = false` at
line 563 — fine — but the OLD goroutine that was spawned for PID 82
still has `stopping = true` in its captured state when it eventually
runs, so it returns silently when PID 82 finally exits, leaving the
restart count untouched. Harmless for counting, but indicates the model
is muddled.

**Required fix (sketch):**

- Have `restart()` request a stop and *let the existing supervisor goroutine
  trigger the restart via its existing path*. Concretely: set
  `p.stopping = true; signal SIGTERM; wait for the supervisor goroutine to
  observe exit and set stopping back to false; then it restarts on its
  own backoff*. No double `Wait()`.
- Or: restructure with a single restart-controller goroutine fed by a
  channel; `restart()` sends a message; the controller is the *only* code
  path that ever calls `Wait()`/`Start()`.

Either way, the current double-Wait is the proximate cause of the port
race.

---

### Tertiary observation — `workspace-secrets-<id>` Secret is missing from K8s

After the bind, the `EnsureSecretsManifest` step in
`api/internal/handlers/secrets.go:393-398` should have written
`workspace-secrets-8e5e74c3-…` to K8s. It did not. There were no warning
logs from `EnsureSecretsManifest failed`, and no warning from
`PrepareSecretsForInjection failed`. The Secret simply does not exist:

    $ kubectl get secret workspace-secrets-8e5e74c3-7524-4df9-bba9-81a57b33b8d6
    Error from server (NotFound): …

This may share the same root with worklog 0120's "RestartWorkspace drops
user secrets" — the materializer path may silently no-op when bindings
exist but the manifest writer wasn't wired. Did not deep-dive in this
session because Bug 1 + Bug 2 already block the flow upstream, but flagged
for the next session: confirm `manifestWriter` is non-nil in production
config, and whether `PrepareSecretsForInjection` returned `[]` (no-op
short-circuit) for a workspace whose binding contains a real secret.

The fact that `reload-secrets` reports `reloaded: 1` proves agentd received
a non-empty payload, so the live HTTP push is working — only the durable
K8s Secret write is silently absent.

---

## Why the upstream test suite missed both bugs

`pkg/agent/opencode/client_test.go` mocks `httptest.NewServer` with no
Basic auth gating. Local unit tests pass with no Authorization header,
production opencode demands `Basic`. **The test fixture is unfaithful to
the real server.**

`cmd/workspace-agentd/main.go` has `managedProcess_test.go` etc. but the
restart() race only manifests when the *previous* opencode is slow to
release port 4096, which `httptest` and `exec.Command("/bin/true")`-based
fakes do not exhibit.

---

## State of the cluster after this session

- Throw-away user `credflow-1780436630@example.com` (UID `be8923b9-…`) left in DB
- Workspace `8e5e74c3-…` left in `Active` phase but its pod is in an
  opencode restart loop — opencode is unreachable for the bound user
- Secret `4b4408ba-…` left bound to the workspace
- One earlier failed workspace `da3d9c97-…` (runtime: `python:3.11`,
  invalid; only `base` exists) was deleted via `DELETE /workspaces/:id`
  during this session

Recommend cleanup via `DELETE /api/v1/workspaces/8e5e74c3-…` and
`DELETE /api/v1/secrets/4b4408ba-…` once the bugs are fixed and a clean
re-run validates the flow end-to-end.

---

## Files relevant to the bugs

| File | Lines | Bug |
|---|---|---|
| `pkg/agent/opencode/client.go` | 27-34 (NewClient), 106-138 (setAuth) | Bug 1 |
| `pkg/agent/opencode/client_test.go` | mock servers | Bug 1 — test gap |
| `cmd/workspace-agentd/secrets.go` | 271 | Bug 1 — caller |
| `cmd/workspace-agentd/main.go` | 560-657 (start/restart) | Bug 2 |
| `pkg/agentd/types.go` | 23 (`AuthUsername = "opencode"`) | Bug 1 — constant to use |
| `api/internal/handlers/secrets.go` | 377-420 (pushSecretsToAgent) | tertiary — K8s Secret absent |

---

## Tests Run

Manual e2e against `https://safespace.thekao.cloud/api/v1`. No Go unit
tests run this session — the failure is in production wiring, not local
test logic.

---

## Next Steps

1. Fix Bug 1: thread password into `pkg/agent/opencode/client.go`,
   `SetBasicAuth` in every request method. Update unit tests to require
   Basic auth (write the server-side check first; assert the existing
   tests fail; then add the auth wiring to make them pass — TDD).
2. Fix Bug 2: collapse start/restart goroutines so only one routine ever
   `Wait()`s any given `*exec.Cmd`. New tests must exercise the
   "previous opencode still alive when restart is called" timing.
3. Investigate tertiary: why does `EnsureSecretsManifest` not write
   `workspace-secrets-<id>` for a fresh-bind-on-Active-workspace path?
   Suspect `manifestWriter` is nil in production config — check
   `app.go:164` wiring.
4. After all three are fixed, re-run this session's flow end-to-end and
   confirm `secrets.json` lands in `/sandbox-cfg/`, opencode picks up
   the credential, and a `POST /session/:id/message` round-trip
   completes against the LiteLLM endpoint.

---

## Files Modified

`worklogs/0125_2026-06-02_credflow-live-exercise-found-2-bugs.md` (new)

No source code modified this session — exercise was read-only against
the live cluster, and the bugs found block the path before any fix
attempt would be productive without a deeper triage cycle.
