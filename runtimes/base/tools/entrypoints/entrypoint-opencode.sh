#!/usr/bin/env bash
set -euo pipefail
source /usr/local/bin/entrypoint-common.sh

eval "$(mise activate bash)"

# Source environment secrets materialized by entrypoint-common.sh
if [[ -f /sandbox-cfg/env ]]; then
    source /sandbox-cfg/env
fi

export OPENCODE_CONFIG=/tmp/agent-config.json
export XDG_DATA_HOME=/workspace/.local

if [[ -f /sandbox-cfg/password ]]; then
    export OPENCODE_SERVER_PASSWORD="$(cat /sandbox-cfg/password)"
fi

workspace-agentd &

exec opencode serve --hostname 0.0.0.0 --port 4096
