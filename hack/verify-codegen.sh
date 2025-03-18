#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

# Save current generated files
TMP_DIR=$(mktemp -d)
cp -R "${SCRIPT_ROOT}/src" "${TMP_DIR}/"

# Run the update script
"${SCRIPT_ROOT}/hack/update-codegen.sh"

echo "Comparing generated files..."
diff -Naupr "${SCRIPT_ROOT}/src" "${TMP_DIR}/src" || {
  echo "Generated files are not up-to-date. Please run hack/update-codegen.sh"
  exit 1
}
echo "Generated files verified."
