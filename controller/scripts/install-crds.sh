#!/bin/bash

# Exit on error
set -e

# Generate CRD manifests
make manifests

# Apply CRDs to the cluster
kubectl apply -f config/crd/bases/

echo "CRDs installed successfully"
