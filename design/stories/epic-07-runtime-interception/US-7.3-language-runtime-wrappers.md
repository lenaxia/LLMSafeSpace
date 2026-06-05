# US-7.3: Language Runtime Wrappers

**Epic:** 7 — Runtime Interception Layer
**Status:** Redesigned — implement as env-var injection (PYTHONSTARTUP, NODE_OPTIONS); see issue #41
**Dependencies:** US-7.4 (RuntimePolicy CRD — for policy config format)

## Objective

Build compiled Go wrapper binaries that replace language runtime binaries (python3, node, go) at their canonical paths. Wrappers apply security policies (restricted modules, import hooks, seccomp additions) before exec'ing the real binary. No daemon involvement — policy enforcement is inline.

## Design

### Interception Model

Unlike package manager wrappers (which forward to the daemon for root), language wrappers:
1. Read policy config from `/etc/llmsafespace/policies/<language>/config.json`
2. Apply environment-level restrictions (env vars, LD_PRELOAD, etc.)
3. Exec the real binary with the modified environment

No socket, no daemon, no root. The wrapper runs as the sandbox user and simply interposes policy before execution.

### Policy Application Per Language

#### Python

```go
func applyPythonPolicy(policy *PolicyConfig, args []string, env []string) ([]string, []string) {
    // 1. Set PYTHONSTARTUP to import hook that blocks restricted modules
    env = append(env, "PYTHONSTARTUP=/opt/llmsafespace/policies/python/startup.py")
    
    // 2. Set PYTHONPATH to include sitecustomize that enforces restrictions
    env = append(env, "PYTHONPATH=/opt/llmsafespace/policies/python/site")
    
    // 3. Disable -c/-m for blocked modules if policy requires
    // (e.g., block `python -c "import ctypes"`)
    
    return args, env
}
```

The `startup.py` / `sitecustomize.py` files (already exist from V1 as `runtimes/python/security/python/sitecustomize.py`) hook `__import__` to block restricted modules at runtime.

#### Node.js

```go
func applyNodePolicy(policy *PolicyConfig, args []string, env []string) ([]string, []string) {
    // 1. Prepend --require to load restriction module
    args = append([]string{"--require", "/opt/llmsafespace/policies/nodejs/restrict.js"}, args...)
    
    // 2. Set NODE_OPTIONS for additional restrictions
    env = append(env, "NODE_OPTIONS=--disallow-code-generation-from-strings")
    
    return args, env
}
```

The `restrict.js` file (already exists as `runtimes/nodejs/tools/nodejs-security-wrapper.js`) patches `require()` to block restricted modules.

#### Go

```go
func applyGoPolicy(policy *PolicyConfig, args []string, env []string) ([]string, []string) {
    // Go is compiled — runtime restrictions are limited.
    // Policy focuses on build-time:
    // 1. Block `go get` from non-allowlisted sources (GONOSUMCHECK, GOFLAGS)
    // 2. Enforce -trimpath for builds
    // 3. Block CGO if policy requires
    if policy.DisableCGO {
        env = append(env, "CGO_ENABLED=0")
    }
    
    return args, env
}
```

### Policy Config Format

```json
{
  "language": "python",
  "enabled": true,
  "restrictedModules": ["ctypes", "subprocess", "os.system"],
  "allowedSources": ["https://pypi.org/simple/"],
  "seccompAdditions": "/opt/llmsafespace/policies/python/seccomp.json",
  "env": {
    "PYTHONDONTWRITEBYTECODE": "1"
  }
}
```

Loaded from `/etc/llmsafespace/policies/<language>/config.json`. If the file doesn't exist or `enabled: false`, the wrapper is a pure passthrough (exec real binary with no modifications).

### No-Policy Passthrough

Two levels of passthrough:

1. **No sentinel** (`/etc/llmsafespace/mode` absent) — Docker mode. Wrapper exec's real binary immediately. Zero policy enforcement.
2. **Sentinel present but no policy for this language** — Wrapper exec's real binary. No restrictions for unsupported runtimes.

```go
// Handled by multi-call binary (cmd/wrapper/main.go)
// Language runtime dispatch:
func handleLanguageRuntime(name string) {
    realBin := filepath.Join("/opt/llmsafespace/.bin", name)
    
    policy, err := loadPolicy(name)
    if err != nil || !policy.Enabled {
        // No policy or disabled — pure passthrough
        syscall.Exec(realBin, os.Args, os.Environ())
    }
    
    args, env := applyPolicy(name, policy, os.Args[1:], os.Environ())
    syscall.Exec(realBin, append([]string{name}, args...), env)
}
```

### Shared Multi-Call Binary

The wrapper binary from US-7.2 handles both package managers and language runtimes. The main dispatch in `cmd/wrapper/main.go` routes based on `argv[0]`. This story implements the `handleLanguageRuntime` path.

## Files Modified/Created

| File | Purpose |
|------|---------|
| `cmd/wrapper/runtime.go` | Language runtime dispatch: load policy, apply, exec |
| `cmd/wrapper/policy.go` | Policy config loading and parsing |
| `cmd/wrapper/python.go` | Python-specific policy application |
| `cmd/wrapper/nodejs.go` | Node.js-specific policy application |
| `cmd/wrapper/golang.go` | Go-specific policy application |
| `runtimes/base/policies/python/startup.py` | Import hook (migrated from `runtimes/python/security/`) |
| `runtimes/base/policies/python/restricted_modules.json` | Module blocklist (migrated) |
| `runtimes/base/policies/nodejs/restrict.js` | Require hook (migrated from `runtimes/nodejs/tools/`) |
| `runtimes/base/policies/nodejs/restricted_modules.json` | Module blocklist (migrated) |

## Acceptance Criteria

1. `python3 -c "print('hello')"` works (passthrough when no restricted modules hit)
2. `python3 -c "import ctypes"` is blocked when `ctypes` is in restricted list
3. `node -e "require('child_process')"` is blocked when restricted
4. `go build ./...` works with policy-enforced `-trimpath`
5. With no policy config file, all runtimes pass through with zero overhead
6. Wrapper adds <1ms latency (measured: time between wrapper start and exec syscall)
7. Policy changes (ConfigMap update → file change) take effect on next invocation (no caching across invocations)
