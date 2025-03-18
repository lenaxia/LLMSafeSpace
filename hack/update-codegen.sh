#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=${CODEGEN_PKG:-$(go env GOPATH)/pkg/mod/k8s.io/code-generator@v0.28.4}

# Generate deepcopy functions
"${CODEGEN_PKG}/generate-groups.sh" "deepcopy" \
  github.com/llmsafespace/pkg/client \
  github.com/llmsafespace/src/pkg \
  "types:v1" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  --output-base "${SCRIPT_ROOT}"

# Alternative direct approach if the above doesn't work
"${CODEGEN_PKG}/kube_codegen.sh" deepcopy \
  --input-dirs github.com/llmsafespace/src/pkg/types \
  --output-base "${SCRIPT_ROOT}" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt"
