#!/usr/bin/env bash
set -euo pipefail
which redact
which bash
which curl
which git
which jq
which opencode
which mise
# Verify mise system-installed runtime package managers are on PATH via shims
mise --system which python
mise --system which pip
mise --system which node
mise --system which npm
mise --system which cargo
mise --system which gem
mise --system which go
mise --system which java
mise --system which mvn
mise --system which gradle
