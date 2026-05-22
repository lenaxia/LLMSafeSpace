#!/usr/bin/env bash
set -euo pipefail
source /usr/local/bin/entrypoint-common.sh

export OPENCODE_CONFIG=/tmp/agent-config.json
export XDG_DATA_HOME=/workspace/.local

if [[ -f /sandbox-cfg/password ]]; then
    export OPENCODE_SERVER_PASSWORD="$(cat /sandbox-cfg/password)"
fi

exec opencode serve --hostname 0.0.0.0 --port 4096
