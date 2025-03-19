 #!/usr/bin/env bash

 set -o errexit
 set -o nounset
 set -o pipefail

 SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

 # Source the helper functions
 source "${SCRIPT_ROOT}/hack/kube_codegen.sh"

 # Call the helper function with the correct path
 # Use the local path rather than the import path
 kube::codegen::gen_helpers "${SCRIPT_ROOT}"

