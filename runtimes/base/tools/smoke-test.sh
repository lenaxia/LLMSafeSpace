#!/usr/bin/env bash
set -euo pipefail
which redact
which bash
which curl
which git
which jq
which opencode
which mise
mise which python
mise which pip
mise which node
mise which npm
mise which cargo
mise which go
# JVM tools may not be pre-installed on arm64 (mise java plugin issue);
# they will be installed on-demand at runtime via mise.
mise which java || echo "WARN: java not pre-installed (available via mise at runtime)"
mise which mvn || echo "WARN: mvn not pre-installed (available via mise at runtime)"
mise which gradle || echo "WARN: gradle not pre-installed (available via mise at runtime)"
