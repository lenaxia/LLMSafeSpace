#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/../src/api
MODULE_PATH="github.com/lenaxia/llmsafespace/api"
TYPES_PKG="${MODULE_PATH}/internal/types"

# Generate deepcopy functions with verbose logging
"$(go env GOPATH)/bin/deepcopy-gen" \
  --v 5 \
  --input-dirs "${TYPES_PKG}" \
  --output-package "${TYPES_PKG}" \
  --output-file-base zz_generated.deepcopy \
  --bounding-dirs "${TYPES_PKG}" \
  --go-header-file "${SCRIPT_ROOT}/../hack/boilerplate.go.txt"

# Format the generated code
go fmt "./src/api/internal/types/zz_generated.deepcopy.go"

echo "DeepCopy generation completed successfully"
