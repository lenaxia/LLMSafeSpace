#!/usr/bin/env bash
set -euo pipefail

# Internal binaries (built into the image)
which redact
which workspace-agentd

# Core shell + dev tools (apt)
which bash
which curl
which file
which git
which gnupg || which gpg
which jq
which less
which make
which gcc
which openssl
which ps
which rsync
which sqlite3
which ssh
which ssh-keygen
which vim.tiny || which vim

# DB clients
which psql
which mysql

# Cloud CLIs
which aws

# Agent runtime
which opencode
which mise

# Language runtimes (mise-managed, baked into image layer)
mise which python
mise which pip
mise which node
mise which npm
mise which cargo
mise which go
# JVM tools may not be pre-installed on arm64 (mise java plugin issue);
# they will be installed on-demand at runtime via mise.
mise which java   || echo "WARN: java not pre-installed (available via mise at runtime)"
mise which mvn    || echo "WARN: mvn not pre-installed (available via mise at runtime)"
mise which gradle || echo "WARN: gradle not pre-installed (available via mise at runtime)"
