#!/usr/bin/env bash
set -euo pipefail
source /usr/local/bin/entrypoint-common.sh

eval "$(mise activate bash)"

# Source environment secrets materialized by entrypoint-common.sh
if [[ -f /sandbox-cfg/env ]]; then
    source /sandbox-cfg/env
fi
# Also source hot-reloaded env if present
# Path must match pkg/agentd/types.go SecretsEnvPath
if [[ -f /tmp/secrets-env ]]; then
    source /tmp/secrets-env
fi

export OPENCODE_CONFIG=/tmp/agent-config.json
export XDG_DATA_HOME=/workspace/.local

if [[ -f /sandbox-cfg/password ]]; then
    export OPENCODE_SERVER_PASSWORD="$(cat /sandbox-cfg/password)"
fi

# agentd is PID 1 (supervisor). It manages opencode as a child process.
exec workspace-agentd --supervise
