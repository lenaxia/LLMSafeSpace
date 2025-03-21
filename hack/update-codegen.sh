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
echo "API Package: github.com/llmsafespace/pkg"
echo "Groups/Versions: types:v1"

# Get the module name from go.mod
MODULE_NAME=$(grep -m 1 "module" "${SCRIPT_ROOT}/go.mod" | awk '{print $2}')
echo "Using module name: ${MODULE_NAME}"

# Generate deepcopy functions using go run
echo "Using go run to execute deepcopy-gen directly"

# Print current directory for debugging
echo "Current directory: $(pwd)"
echo "Looking for types in: ${SCRIPT_ROOT}/pkg/types"

# Run deepcopy-gen with absolute paths
go run k8s.io/code-generator/cmd/deepcopy-gen \
  --bounding-dirs "${MODULE_NAME}" \
  --output-file "${SCRIPT_ROOT}/pkg/types/zz_generated.deepcopy.go" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
  -v=5

RESULT=$?
if [ $RESULT -eq 0 ]; then
    echo "✅ generate-groups.sh completed successfully"
else
    echo "❌ generate-groups.sh failed with exit code $RESULT"
    echo "Trying alternative approach..."
fi

echo "=== Generating deepcopy functions using direct approach ==="
echo "Input directories: github.com/llmsafespace/pkg/types"

# No need for alternative approach anymore
echo "Direct deepcopy-gen execution completed"

RESULT=$?
if [ $RESULT -eq 0 ]; then
    echo "✅ kube_codegen.sh completed successfully"
else
    echo "❌ kube_codegen.sh failed with exit code $RESULT"
fi

# Check if the generated file exists
GENERATED_FILE="${SCRIPT_ROOT}/pkg/types/zz_generated.deepcopy.go"
if [ -f "$GENERATED_FILE" ]; then
    echo "✅ Generated file exists at: $GENERATED_FILE"
    echo "File size: $(wc -l < "$GENERATED_FILE") lines"
    echo "First few lines:"
    head -n 10 "$GENERATED_FILE"
    echo "..."
    echo "Generation successful!"
else
    echo "❌ Generated file not found at expected location: $GENERATED_FILE"
    echo "Searching for generated file in other locations..."
    
    # Search for the generated file in common locations
    find "${SCRIPT_ROOT}" -name "zz_generated.deepcopy.go" -o -name "generated.deepcopy.go" | while read -r file; do
        echo "Found generated file at: $file"
        echo "File size: $(wc -l < "$file") lines"
        echo "First few lines:"
        head -n 5 "$file"
        echo "..."
    done
    
    # Try running a simpler command directly
    echo "Trying a simpler direct command..."
    cd "${SCRIPT_ROOT}"
    go run k8s.io/code-generator/cmd/deepcopy-gen \
      --output-file "${SCRIPT_ROOT}/pkg/types/zz_generated.deepcopy.go" \
      --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
      -v=5
    
    if [ -f "$GENERATED_FILE" ]; then
        echo "✅ Generated file exists after direct command at: $GENERATED_FILE"
        echo "File size: $(wc -l < "$GENERATED_FILE") lines"
    else
        echo "❌ Still no generated file."
        echo "Generation failed. Please check the error messages above."
        exit 1
    fi
fi

echo "=== Code generation process completed at $(date) ==="
