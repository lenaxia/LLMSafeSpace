#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

# Source the helper functions
source "${SCRIPT_ROOT}/hack/kube_codegen.sh"

# Call the helper function
# The argument should be the root directory where your Go files are located
kube::codegen::gen_helpers "${SCRIPT_ROOT}"

