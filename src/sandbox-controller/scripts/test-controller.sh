#!/bin/bash

# Exit on error
set -e

# Apply CRDs
kubectl apply -f config/crd/bases/

# Create a test warm pool
kubectl apply -f examples/test-warmpool.yaml

# Wait for warm pods to be created
echo "Waiting for warm pods to be created..."
sleep 10

# Check the status of the warm pool
kubectl get warmpool test-python-pool -o yaml

# Check the status of the warm pods
kubectl get warmpods -l pool-name=test-python-pool

# Create a test sandbox
kubectl apply -f examples/test-sandbox.yaml

# Wait for the sandbox to be created
echo "Waiting for sandbox to be created..."
sleep 10

# Check the status of the sandbox
kubectl get sandbox test-sandbox -o yaml

# Check if the sandbox is using a warm pod
WARM_POD_ID=$(kubectl get sandbox test-sandbox -o jsonpath='{.metadata.annotations.llmsafespace\.dev/warm-pod-id}')
if [ -n "$WARM_POD_ID" ]; then
  echo "Sandbox is using warm pod: $WARM_POD_ID"
  kubectl get warmpod $WARM_POD_ID -o yaml
else
  echo "Sandbox is not using a warm pod"
fi

# Clean up
echo "Cleaning up..."
kubectl delete sandbox test-sandbox
kubectl delete warmpool test-python-pool

echo "Test completed successfully"
