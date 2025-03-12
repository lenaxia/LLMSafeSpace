#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=${CODEGEN_PKG:-$(cd "${SCRIPT_ROOT}"; ls -d -1 ./vendor/k8s.io/code-generator 2>/dev/null || echo "$(go env GOPATH)/pkg/mod/k8s.io/code-generator@v0.26.0")}

# Generate deepcopy functions
"$(go env GOPATH)/bin/deepcopy-gen" \
  --input-dirs "github.com/lenaxia/llmsafespace/api/internal/types" \
  --output-package "github.com/lenaxia/llmsafespace/api/internal/types" \
  --output-file-base zz_generated.deepcopy \
  --bounding-dirs "github.com/lenaxia/llmsafespace/api/internal/types" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt"

# Format the generated code
go fmt "${SCRIPT_ROOT}/src/api/internal/types/zz_generated.deepcopy.go"

echo "DeepCopy generation completed successfully"
