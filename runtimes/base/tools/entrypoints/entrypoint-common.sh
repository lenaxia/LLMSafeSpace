#!/usr/bin/env bash
set -euo pipefail
if [[ -f /sandbox-cfg/credentials ]]; then
    cp /sandbox-cfg/credentials /tmp/agent-config.json
else
    echo '{}' > /tmp/agent-config.json
fi
