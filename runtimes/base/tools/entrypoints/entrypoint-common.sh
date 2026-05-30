#!/usr/bin/env bash
# entrypoint-common.sh — Boot-time secret materialization shim.
#
# As of Epic 17 G2 remediation (worklog 0078), all secret materialization
# is performed by the `workspace-agentd materialize` Go subcommand. This
# script is a thin shim that:
#
#   1. Verifies the agentd binary exists in the runtime image (a missing
#      binary is a build error, not a runtime degradation).
#   2. Invokes the materialize subcommand. The subcommand:
#       - reads /sandbox-cfg/secrets.json (no-op if absent),
#       - validates each secret entry against the threat-model invariants
#         in pkg/agentd/secrets,
#       - writes credential files with mode 0600 atomic-on-create,
#       - skips invalid entries (T5: never blocks pod boot for one bad
#         entry), reporting the rejection reason to stderr.
#
# Threat-model invariants this shim preserves:
#
#   T1 No interpretation of secret values by the shell. The materializer
#      is a Go binary; no plaintext ever passes through bash word-splitting.
#   T2 No file ever exists with mode > 0600 for credential material.
#   T5 An invalid secret skips that secret only; the rest still apply.
#
# See pkg/agentd/secrets/secrets.go for the implementation and
# pkg/agentd/secrets/secrets_test.go for the bash-subprocess regression
# corpus that locks these invariants in place.
set -euo pipefail

if ! command -v workspace-agentd >/dev/null 2>&1; then
    echo "entrypoint-common: workspace-agentd binary missing from runtime image" >&2
    exit 1
fi

workspace-agentd materialize
