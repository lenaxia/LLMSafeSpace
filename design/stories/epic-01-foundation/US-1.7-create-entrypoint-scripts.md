# US-1.7: Create Entrypoint Scripts

**Epic:** 1 - Foundation
**Priority:** High

## User Story

As a developer, I want entrypoint scripts that start opencode serve with the correct configuration, so that every sandbox runs the agent server correctly.

## Acceptance Criteria

- [ ] `entrypoint-common.sh` materializes credentials and sets environment
- [ ] `entrypoint-opencode.sh` starts opencode serve with correct flags and env vars
- [ ] Password read from file, exported as OPENCODE_SERVER_PASSWORD
- [ ] XDG_DATA_HOME set to /workspace/.local for session persistence
- [ ] OPENCODE_CONFIG set to /tmp/agent-config.json
- [ ] Scripts are executable and have no bash errors

## Technical Details

**New files:**

| File | Purpose |
|------|---------|
| `runtimes/base/tools/entrypoints/entrypoint-common.sh` | Shared setup: credential materialization, sentinel check |
| `runtimes/base/tools/entrypoints/entrypoint-opencode.sh` | OpenCode agent runner |

**entrypoint-opencode.sh:**

```bash
#!/usr/bin/env bash
set -euo pipefail
source /usr/local/bin/entrypoint-common.sh

export OPENCODE_CONFIG=/tmp/agent-config.json
export XDG_DATA_HOME=/workspace/.local

if [[ -f /sandbox-cfg/password ]]; then
    export OPENCODE_SERVER_PASSWORD=$(cat /sandbox-cfg/password)
fi

exec opencode serve --hostname 0.0.0.0 --port 4096
```

**entrypoint-common.sh:**

```bash
#!/usr/bin/env bash
# Materialize credentials from init container volume to config file
if [[ -f /sandbox-cfg/credentials ]]; then
    cp /sandbox-cfg/credentials /tmp/agent-config.json
else
    echo '{}' > /tmp/agent-config.json
fi
```

**Verified:** Password is env var `OPENCODE_SERVER_PASSWORD` (not a CLI flag). Auth is HTTP Basic Auth. Data dir override is via `XDG_DATA_HOME`. See design §7.1a.

## Design Reference

Section 7.7: Entrypoint Scripts
Section 7.1a: OpenCode API Contract (Verified)

## Effort

Small (1-2 hours)
