#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

echo "=== Starting code generation process ==="
echo "$(date): Beginning deepcopy code generation"

# Get script directory
SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
echo "Script root: ${SCRIPT_ROOT}"

# Find code-generator package
CODEGEN_PKG=${CODEGEN_PKG:-$(go env GOPATH)/pkg/mod/k8s.io/code-generator@v0.32.3}
echo "Using code-generator at: ${CODEGEN_PKG}"

# Check if code-generator exists
if [ ! -d "${CODEGEN_PKG}" ]; then
    echo "ERROR: code-generator package not found at ${CODEGEN_PKG}"
    echo "Please install it with: go get k8s.io/code-generator@v0.32.3"
    exit 1
fi

echo "=== Generating deepcopy functions using generate-groups.sh ==="
echo "Target package: github.com/llmsafespace/pkg/client"
echo "API Package: github.com/llmsafespace/src/pkg"
echo "Groups/Versions: types:v1"

# Get the module name from go.mod
MODULE_NAME=$(grep -m 1 "module" "${SCRIPT_ROOT}/go.mod" | awk '{print $2}')
echo "Using module name: ${MODULE_NAME}"

# Generate deepcopy functions
"${CODEGEN_PKG}/generate-groups.sh" "deepcopy" \
  "${MODULE_NAME}/pkg/client" \
  "${MODULE_NAME}/src/pkg" \
  "types:v1" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  --output-base "${SCRIPT_ROOT}" \
  --v=1

RESULT=$?
if [ $RESULT -eq 0 ]; then
    echo "✅ generate-groups.sh completed successfully"
else
    echo "❌ generate-groups.sh failed with exit code $RESULT"
    echo "Trying alternative approach..."
fi

echo "=== Generating deepcopy functions using direct approach ==="
echo "Input directories: github.com/llmsafespace/src/pkg/types"

# Alternative direct approach if the above doesn't work
"${CODEGEN_PKG}/kube_codegen.sh" deepcopy \
  --input-dirs github.com/llmsafespace/src/pkg/types \
  --output-base "${SCRIPT_ROOT}" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  --v=1 \
  --boilerplate-file="${SCRIPT_ROOT}/hack/boilerplate.go.txt"

RESULT=$?
if [ $RESULT -eq 0 ]; then
    echo "✅ kube_codegen.sh completed successfully"
else
    echo "❌ kube_codegen.sh failed with exit code $RESULT"
fi

# Check if the generated file exists
GENERATED_FILE="${SCRIPT_ROOT}/src/pkg/types/zz_generated.deepcopy.go"
if [ -f "$GENERATED_FILE" ]; then
    echo "✅ Generated file exists at: $GENERATED_FILE"
    echo "File size: $(wc -l < "$GENERATED_FILE") lines"
    echo "First few lines:"
    head -n 5 "$GENERATED_FILE"
else
    echo "❌ Generated file not found at: $GENERATED_FILE"
fi

echo "=== Code generation process completed at $(date) ==="
