# US-7.1: System Daemon

**Epic:** 7 — Runtime Interception Layer
**Status:** Planning
**Dependencies:** None

## Objective

Build a root-owned daemon that runs as PID 1 inside the workspace container. It serves two roles:
1. **Process supervisor** — forks opencode as UID 1000 via gosu, reaps zombies, handles signals
2. **Privilege gateway** — listens on a Unix socket for apt/apk install requests from the agent

## Design

### Process Lifecycle

```
system-daemon (PID 1, root)
  │
  ├── 1. Load policy from /etc/llmsafespace/daemon/policy.json
  ├── 2. Create /run/llmsafespace/system.sock (0660 root:sandbox)
  ├── 3. Fork: gosu sandbox /usr/local/bin/entrypoint-opencode.sh
  │        └── opencode serve (UID 1000, PID >1)
  ├── 4. Accept loop: handle socket requests
  └── 5. On SIGTERM: signal child, wait, exit
```

The daemon is a minimal Go binary. It does not import opencode or any agent logic. Its only responsibilities are:
- Start the agent subprocess as UID 1000
- Listen for privileged install requests
- Reap zombie processes (PID 1 responsibility)
- Forward signals to child (graceful shutdown)

### Socket Protocol

JSON-over-Unix-socket, newline-delimited request/response.

```go
type Request struct {
    ID      string   `json:"id"`
    Command string   `json:"command"`  // "apt", "apk"
    Args    []string `json:"args"`     // ["install", "-y", "python3"]
}

type StreamLine struct {
    ID     string `json:"id"`
    Stream string `json:"stream,omitempty"` // "stdout" or "stderr"
    Data   string `json:"data,omitempty"`
    Done   bool   `json:"done,omitempty"`
    Exit   int    `json:"exit,omitempty"`
    Error  string `json:"error,omitempty"`
}
```

The daemon streams stdout/stderr back line-by-line so the agent sees real-time install progress.

### Policy Engine

Before executing, the daemon validates:

1. **Command allowlist** — only `apt`, `apt-get`, `apk` (configurable)
2. **Subcommand allowlist** — `install`, `update`, `list`, `search` (not `remove` by default)
3. **Blocked flags** — `--allow-unauthenticated`, `--force-yes`, etc.
4. **Blocked packages** — configurable denylist
5. **Source restrictions** — no `--add-repository`, no custom sources.list modifications
6. **Rate limiting** — max N installs per minute (default 10)

Policy loaded from `/etc/llmsafespace/daemon/policy.json`:

```json
{
  "allowedCommands": ["apt", "apt-get", "apk"],
  "allowedSubcommands": ["install", "update", "list", "search", "show"],
  "blockedFlags": ["--allow-unauthenticated", "--force-yes", "--no-check-certificate"],
  "blockedPackages": [],
  "rateLimit": {"maxPerMinute": 10}
}
```

If no policy file exists, the daemon uses a sensible default (allow install/update from official repos only).

### Audit Log

Every request logged to `/var/log/llmsafespace/audit.jsonl`:

```json
{"ts":"2026-05-24T20:00:00Z","id":"abc","command":"apt","args":["install","-y","python3"],"decision":"allow","exitCode":0,"durationMs":3200}
```

### Signal Handling

- `SIGTERM` / `SIGINT` → forward to child process group, wait for exit, then exit
- `SIGCHLD` → reap zombies (PID 1 responsibility)
- If child (opencode) exits unexpectedly → log, exit with child's exit code (let container runtime handle restart)

### Dependencies

- `gosu` (or `su-exec` for Alpine) — for dropping privilege. Statically compiled, no SUID needed since daemon is already root.
- No other external dependencies. Pure Go binary.

## Files Created

| File | Purpose |
|------|---------|
| `cmd/system-daemon/main.go` | Entrypoint: signal setup, socket creation, child fork, accept loop |
| `cmd/system-daemon/child.go` | Fork opencode via gosu, monitor child, reap zombies |
| `cmd/system-daemon/handler.go` | Socket request handling: parse, validate, exec, stream |
| `cmd/system-daemon/policy.go` | Policy loading and evaluation |
| `cmd/system-daemon/audit.go` | Audit log writer |

## Acceptance Criteria

1. Daemon starts as PID 1, creates socket at `/run/llmsafespace/system.sock`
2. Socket permissions are `0660 root:sandbox`
3. Opencode starts as UID 1000 (verified via `ps aux` inside container)
4. `apt install curl` via socket succeeds (installed as root)
5. Blocked flag (`--allow-unauthenticated`) is rejected with clear error
6. Rate limit triggers after N rapid requests
7. Audit log contains all requests with decisions
8. SIGTERM to container → opencode gets signal → graceful shutdown
9. If opencode crashes, daemon exits (container restarts via restart policy)
10. Unit tests: policy allow/deny, rate limit, malformed request, signal forwarding

## Non-Goals

- No TLS on socket (Unix socket + file permissions sufficient)
- No hot-reload of policy (restart container to change policy)
- No package removal in V1 (install-only)
- Daemon does NOT handle pip/npm/cargo — those work as UID 1000 directly
