#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

# Generate deepcopy functions with controller-gen
echo "Generating deepcopy methods with controller-gen..."
go run sigs.k8s.io/controller-tools/cmd/controller-gen \
  object:headerFile="hack/boilerplate.go.txt" \
  paths="./..."

# Generate runtime.Object implementations and clients with code-generator
echo "Generating clients with code-generator..."
source "${SCRIPT_ROOT}/hack/kube_codegen.sh"
kube::codegen::gen_helpers "${SCRIPT_ROOT}"

