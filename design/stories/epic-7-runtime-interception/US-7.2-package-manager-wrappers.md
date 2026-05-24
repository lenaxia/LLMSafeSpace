# US-7.2: Package Manager Wrappers

**Epic:** 7 — Runtime Interception Layer
**Status:** Planning
**Dependencies:** US-7.1 (System Daemon)

## Objective

Build compiled Go wrapper binaries that replace package manager binaries at their canonical paths. Wrappers forward install requests to the system daemon via Unix socket. Real binaries are relocated to a hidden directory.

## Design

### Binary Relocation

During image build (Dockerfile):

```dockerfile
RUN mv /usr/bin/apt /opt/llmsafespace/.bin/apt && \
    mv /usr/bin/pip3 /opt/llmsafespace/.bin/pip3 && \
    mv /usr/local/bin/npm /opt/llmsafespace/.bin/npm
# Install wrappers at original paths
COPY --chmod=555 wrapper /usr/bin/apt
COPY --chmod=555 wrapper /usr/bin/pip3
COPY --chmod=555 wrapper /usr/local/bin/npm
# Make wrappers immutable
RUN chattr +i /usr/bin/apt /usr/bin/pip3 /usr/local/bin/npm
```

`/opt/llmsafespace/.bin/` is:
- Not in any PATH
- Owned by root, permissions `0750 root:root`
- Not directly executable by sandbox user

### Single Binary, Multi-Call

One compiled Go binary, behavior determined by `argv[0]` (like BusyBox):

```go
func main() {
    name := filepath.Base(os.Args[0]) // "apt", "pip3", "npm", etc.
    
    conn, err := net.Dial("unix", "/run/llmsafespace/system.sock")
    // ... send Request{Command: name, Args: os.Args[1:]}
    // ... stream stdout/stderr to os.Stdout/os.Stderr
    // ... exit with response exit code
}
```

Symlinks or hardlinks at each path all point to the same binary. The binary:
1. Connects to the daemon socket
2. Sends the command name + args
3. Streams output back to the caller's stdout/stderr
4. Exits with the daemon-reported exit code

### Error UX

When the daemon rejects a request:

```
$ apt install --allow-unauthenticated sketchy-pkg
[llmsafespace] Blocked: flag '--allow-unauthenticated' is not permitted.
[llmsafespace] Policy: /etc/llmsafespace/daemon/policy.json
[llmsafespace] To request an exception, contact your workspace administrator.
```

When the daemon is unreachable:

```
$ pip install requests
[llmsafespace] Error: cannot connect to system daemon at /run/llmsafespace/system.sock
[llmsafespace] The system daemon may not be running. Contact your administrator.
```

### Covered Package Managers

| Binary | Original Path | Notes |
|--------|--------------|-------|
| `apt` | `/usr/bin/apt` | Also `apt-get` (symlink to same wrapper) |
| `pip` / `pip3` | `/usr/bin/pip3` | Also handles `pip` symlink |
| `npm` | `/usr/local/bin/npm` | |
| `cargo` | `/usr/local/bin/cargo` | If Rust is installed |
| `go install` | Handled by language wrapper (US-7.3) | `go install` needs root for global; `go build` does not |

### What Passes Through vs. What Goes to Daemon

Not all invocations need root. The wrapper checks:
- `pip install` → daemon (needs root for system packages)
- `pip list` → direct exec of real binary (read-only, no root needed)
- `apt install` → daemon
- `apt list` → direct exec
- `npm install -g` → daemon (global install needs root)
- `npm install` (local) → direct exec (writes to cwd, no root needed)

```go
func needsPrivilege(command string, args []string) bool {
    switch command {
    case "apt", "apt-get":
        return containsAny(args, "install", "update", "upgrade", "remove")
    case "pip", "pip3":
        return containsAny(args, "install", "uninstall")
    case "npm":
        return contains(args, "-g") || contains(args, "--global")
    }
    return false
}
```

If no privilege needed, the wrapper exec's the real binary directly (no socket round-trip).

## Files Created

| File | Purpose |
|------|---------|
| `cmd/wrapper/main.go` | Multi-call binary: detect argv[0], route to daemon or direct exec |
| `cmd/wrapper/client.go` | Unix socket client: connect, send request, stream response |
| `cmd/wrapper/privilege.go` | `needsPrivilege()` logic per package manager |

## Acceptance Criteria

1. `apt install python3` succeeds (forwarded to daemon, installed as root)
2. `apt list --installed` succeeds without daemon (direct exec)
3. `pip install requests` succeeds (forwarded to daemon)
4. `pip list` succeeds without daemon (direct exec)
5. `npm install -g typescript` succeeds (forwarded to daemon)
6. `npm install express` (local) succeeds without daemon (direct exec)
7. Wrapper binary is <5MB static binary
8. Wrapper adds <1ms latency for direct-exec path
9. Wrapper is immutable (`lsattr` shows `i` flag)
10. Real binaries are not accessible to sandbox user directly
