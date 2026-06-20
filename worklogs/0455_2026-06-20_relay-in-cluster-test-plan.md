# Worklog: In-Cluster Test Plan for Relay Fleet (Epic 42)

**Date:** 2026-06-20
**Session:** Document all remaining in-cluster validation gaps for the relay fleet
**Status:** Planning — these tests require a Kubernetes cluster (kind/eks/gke) not available in the sandbox

---

## Context

The relay fleet has been validated at every layer EXCEPT the full in-cluster reconcile loop:

| Layer | Validated? | How |
|---|---|---|
| relay-proxy binary → Zen | ✅ | Manual EC2 (worklog 0440) + E2E integration test (worklog 0450) |
| AWS driver Provision/Destroy | ✅ | E2E integration test against real EC2 (worklog 0450) |
| Cloud-init download + SHA-256 + token | ✅ | E2E integration test (worklog 0450) |
| Token auth (X-Relay-Token) | ✅ | Unit + integration test |
| Controller reconciler logic | ✅ | Unit tests with stub driver |
| relay-router forwarding logic | ✅ | Unit + integration tests |
| peers.json ConfigMap sync | ✅ | Unit tests with fake K8s client |
| **Full reconcile: CR → controller → provision → peers → router → relay → Zen** | ❌ | **Never run against a real cluster** |
| **Helm chart renders + deploys correctly** | ❌ | **CI skips (no helm binary); never deployed to kind** |
| **Workspace pod → relay-router → relay-proxy → Zen** | ❌ | **Never tested end-to-end** |
| **Health checking against real relay VMs** | ❌ | **Unit tests only** |
| **429 rotation (destroy + reprovision)** | ❌ | **Unit tests only** |
| **Fallback to Zen-direct when all relays down** | ❌ | **Unit tests only** |

This worklog defines the test plan to close those gaps. Each test requires a running Kubernetes cluster.

---

## Prerequisites

```bash
# 1. Kind cluster
kind create cluster --name relay-test

# 2. Helm install with relay enabled
helm install llmsafespaces ./charts/llmsafespaces \
  -n llmsafespaces --create-namespace \
  --set controller.inferenceRelay.enabled=true \
  --set controller.inferenceRelay.artifact.urls="{https://github.com/lenaxia/LLMSafeSpaces/releases/download/v0.1.0-relay}" \
  --set controller.inferenceRelay.artifact.sha256Arm64="671c46c6c3c1b0afabe9fcdf4c815f4c0e08fe2c28d5d6eff988ba20900b2fc8" \
  --set controller.inferenceRelay.artifact.sha256Amd64="ac12e27bf3a565781749b3bde5d0ff7062e362da259f9702e7852f351b731155"

# 3. AWS credentials secret
kubectl -n llmsafespaces create secret generic aws-relay-irwa \
  --from-literal=accessKeyId=$AWS_ACCESS_KEY_ID \
  --from-literal=secretAccessKey=$AWS_SECRET_ACCESS_KEY

# 4. InferenceRelay CR
cat <<EOF | kubectl apply -f -
apiVersion: llmsafespaces.dev/v1
kind: InferenceRelay
metadata:
  name: relay-fleet
spec:
  upstreamURL: "https://opencode.ai/zen/v1"
  providers:
    - provider: aws
      region: us-east-1
      credentialsRef:
        name: aws-relay-irwa
EOF
```

---

## Test Plan

### Test 1: Helm chart deploys cleanly

**What:** `helm install` produces all expected resources, relay-router pod starts, controller pod starts with relay flags.

**Steps:**
```bash
# Verify relay-router deployment
kubectl -n llmsafespaces get deploy relay-router
kubectl -n llmsafespaces get pods -l app.kubernetes.io/component=relay-router
# Should be 1/1 Running, 1 container (no WG sidecar)

# Verify controller has relay flags
kubectl -n llmsafespaces get deploy llmsafespaces-controller -o jsonpath='{.spec.template.spec.containers[0].args}' | jq .
# Must contain: --enable-inference-relay=true, --relay-artifact-url=..., --relay-artifact-sha256-*

# Verify NO WG resources
kubectl -n llmsafespaces get configmap relay-router-wg-scripts 2>&1 | grep NotFound
kubectl -n llmsafespaces get svc -l app.kubernetes.io/component=relay-router | grep -v relay-router
# Only TCP 8080 ClusterIP Service, no UDP

# Verify namespace is PSA restricted (not privileged)
kubectl get ns llmsafespaces --show-labels | grep pod-security
# Must show: pod-security.kubernetes.io/enforce=restricted
```

**Pass criteria:** relay-router pod Running with 1 container; controller has relay flags; no WG resources; namespace restricted.

---

### Test 2: Controller provisions a relay VM from InferenceRelay CR

**What:** Applying an InferenceRelay CR triggers the controller to call `AWSDriver.Provision()`, write the instance to status, generate a token, and sync peers.json.

**Steps:**
```bash
# Apply the CR (from prerequisites above)
# Wait for provisioning
kubectl get inferencerelay relay-fleet -w

# Verify status shows a provisioning instance
kubectl get inferencerelay relay-fleet -o jsonpath='{.status.instances}' | jq .
# Must contain: {id: "i-...", provider: "aws", publicIP: "x.x.x.x", state: "provisioning"}

# Verify token secret was created
kubectl -n llmsafespaces get secret relay-vm-tokens -o jsonpath='{.data.aws}' | base64 -d
# Must be a 64-char hex string

# Verify peers.json ConfigMap was written
kubectl -n llmsafespaces get configmap relay-router-peers -o jsonpath='{.data.peers\.json}' | jq .
# Must contain: {id: "i-...", endpoint: "x.x.x.x:8080", provider: "aws", state: "healthy", token: "..."}

# Verify the EC2 instance exists and has the SG
aws ec2 describe-instances --instance-ids <id> --query 'Reservations[0].Instances[0].[State.Name,SecurityGroups[0].GroupName]' --output text
# Must show: running, llmsafespaces-relay-proxy
```

**Pass criteria:** CR status shows instance with public IP; token Secret exists; peers.json has correct endpoint+token; EC2 is running with the relay SG.

---

### Test 3: relay-router picks up peers and forwards to relay VM

**What:** The relay-router pod reads peers.json from the ConfigMap, health-checks the relay VM, and marks it healthy.

**Steps:**
```bash
# Check router logs for peer pickup
kubectl -n llmsafespaces logs deploy/relay-router | grep -i peer
# Should show peer config loaded

# Check router metrics
kubectl -n llmsafespaces port-forward deploy/relay-router 8080:8080 &
curl -s http://localhost:8080/metrics | grep relay_router_relay_healthy
# Must show: relay_router_relay_healthy{id="i-...",provider="aws"} 1

# Direct test through the router
curl -s http://localhost:8080/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer public' \
  -d '{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"Say OK"}],"max_tokens":10}'
# Must return HTTP 200 with a completion

# Verify token was sent (check relay-proxy metrics on the VM)
curl -s http://<relay-vm-public-ip>:8080/metrics | grep relay_requests_total
# Must show: relay_requests_total{status="200"} 1
```

**Pass criteria:** router health-checks relay VM; metrics show healthy=1; completion request through router succeeds; relay-proxy on VM received the request (metrics incremented).

---

### Test 4: Workspace pod routes free-model traffic through relay-router

**What:** A workspace pod's agentd injects the relay-router URL into agent-config.json, and a free-model completion flows: workspace → relay-router → relay-proxy → Zen.

**Steps:**
```bash
# Create a workspace
kubectl apply -f - <<EOF
apiVersion: llmsafespaces.dev/v1
kind: Workspace
metadata:
  name: relay-test-ws
spec:
  owner: test-user
  runtime: base
EOF

# Wait for it to be active
kubectl wait workspace relay-test-ws --for=condition=Ready --timeout=300s

# Check the workspace pod has INFERENCE_RELAY_BASEURL set
kubectl get pod -l llmsafespaces.dev/workspace=relay-test-ws -o jsonpath='{.items[0].spec.containers[0].env}' | jq '.[] | select(.name=="INFERENCE_RELAY_BASEURL")'
# Must show: http://relay-router.llmsafespaces.svc.cluster.local:8080

# Send a message through the API (or directly via opencode in the pod)
kubectl exec relay-test-ws-pod -- curl -s http://localhost:4096/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"Say OK"}],"max_tokens":10}'
# Must return a completion (proving workspace → router → relay → Zen path)

# Verify via relay-proxy metrics
curl -s http://<relay-vm-public-ip>:8080/metrics | grep relay_requests_total
# Counter must have incremented
```

**Pass criteria:** workspace pod has INFERENCE_RELAY_BASEURL pointing at relay-router; completion request from inside the workspace succeeds; relay-proxy metrics show the request.

---

### Test 5: Health checking + unhealthy relay exclusion

**What:** When a relay VM goes down, the router marks it unhealthy and stops forwarding.

**Steps:**
```bash
# Get the relay VM instance ID
RELAY_ID=$(kubectl get inferencerelay relay-fleet -o jsonpath='{.status.instances[0].id}')

# Destroy the relay VM directly (simulating failure)
aws ec2 terminate-instances --instance-ids $RELAY_ID

# Wait for router health-check to fail (3 consecutive failures × 15s interval = ~45s)
sleep 60

# Check router metrics
kubectl -n llmsafespaces port-forward deploy/relay-router 8080:8080 &
curl -s http://localhost:8080/metrics | grep relay_router_relay_healthy
# Must show: relay_router_relay_healthy{id="...",provider="aws"} 0

# Try a completion — should hit fallback (Zen-direct, rate-limited)
curl -s http://localhost:8080/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer public' \
  -d '{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'
# Should still return 200 (fallback mode) but with X-Relay-Status: fallback header

# Verify fallback is active
curl -s http://localhost:8080/metrics | grep relay_router_fallback_active
# Must show: relay_router_fallback_active 1
```

**Pass criteria:** router marks relay unhealthy within 60s; fallback activates; requests still succeed (via Zen-direct) with fallback header.

---

### Test 6: Controller re-provisions on relay failure

**What:** The controller detects the unhealthy relay, destroys it, and provisions a replacement.

**Steps:**
```bash
# After Test 5 destroyed the relay VM, wait for controller to re-provision
# (replacementTimeout default is 15m, but for testing set it lower in the CR)
kubectl get inferencerelay relay-fleet -w
# After ~15m (or whatever replacementTimeout is set to), status should show
# a new instance with a new public IP

# Verify the old instance is terminated
aws ec2 describe-instances --instance-ids $RELAY_ID --query 'Reservations[0].Instances[0].State.Name' --output text
# Must show: terminated

# Verify a new instance was provisioned
kubectl get inferencerelay relay-fleet -o jsonpath='{.status.instances[0].id}'
# Must show a different instance ID

# Verify new peers.json was written with the new endpoint
kubectl -n llmsafespaces get configmap relay-router-peers -o jsonpath='{.data.peers\.json}' | jq .relays[0].endpoint
# Must show the new public IP

# Verify router picks up the new peer and marks it healthy
sleep 20
curl -s http://localhost:8080/metrics | grep relay_router_relay_healthy
# Must show: relay_router_relay_healthy{id="new-id",provider="aws"} 1
```

**Pass criteria:** controller destroys failed instance; provisions replacement; updates peers.json; router picks up new peer and routes traffic to it.

---

### Test 7: 429 rotation (destroy + reprovision on rate-limit storm)

**What:** When Zen returns 429s from the relay VM's IP, the router detects the storm, drains the relay, and the controller rotates it.

**Steps:**
```bash
# This requires Zen to actually 429 the relay VM's IP. This happens naturally
# after enough free-model requests from the same IP. To force it:
# 1. Send ~100 rapid requests through the router
for i in $(seq 1 100); do
  curl -s http://localhost:8080/chat/completions \
    -H 'Content-Type: application/json' \
    -H 'Authorization: Bearer public' \
    -d '{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"hi"}],"max_tokens":1}' &
done; wait

# Check router metrics for 429 detection
curl -s http://localhost:8080/metrics | grep 'relay_router_requests_429_total'
# Should show elevated 429 count

# Check if relay was marked draining
curl -s http://localhost:8080/metrics | grep relay_router_relay_healthy
# May show 0 (unhealthy/draining) if 429 threshold exceeded

# Verify the InferenceRelay status reflects draining
kubectl get inferencerelay relay-fleet -o jsonpath='{.status.instances[0].state}'
# May show: draining or quota-exhausted
```

**Pass criteria:** router detects 429 storm; marks relay draining; controller triggers rotation. (This test is hard to force deterministically — Zen's 429 behavior is per-IP and depends on their rate limits. May need to mock or accept it as best-effort.)

---

### Test 8: CR deletion cleans up all relay VMs

**What:** Deleting the InferenceRelay CR triggers the finalizer, which destroys all relay VMs before removing the CR.

**Steps:**
```bash
# Get current instance IDs
INSTANCES=$(kubectl get inferencerelay relay-fleet -o jsonpath='{.status.instances[*].id}')
echo "Instances to destroy: $INSTANCES"

# Delete the CR
kubectl delete inferencerelay relay-fleet

# Wait for deletion to complete (finalizer runs handleDeletion)
kubectl get inferencerelay relay-fleet -w
# Should disappear after all instances are destroyed

# Verify all EC2 instances are terminated
for id in $INSTANCES; do
  aws ec2 describe-instances --instance-ids $id --query 'Reservations[0].Instances[0].State.Name' --output text
  # Must show: terminated
done

# Verify peers.json ConfigMap is gone (owner reference → GC)
kubectl -n llmsafespaces get configmap relay-router-peers 2>&1
# Should show: NotFound (or owned by the deleted CR → garbage collected)

# Verify token Secret is gone
kubectl -n llmsafespaces get secret relay-vm-tokens 2>&1
# Should show: NotFound
```

**Pass criteria:** CR deletion terminates all relay VMs; finalizer is removed; owned resources (ConfigMap, Secret) are garbage collected.

---

### Test 9: Multiple providers (AWS + OCI)

**What:** A fleet with both AWS and OCI providers provisions relays on both clouds, and the router prefers AWS.

**Steps:**
```bash
# Requires OCI credentials secret
kubectl -n llmsafespaces create secret generic oci-credentials \
  --from-literal=tenancy=$OCI_TENANCY \
  --from-literal=user=$OCI_USER \
  --from-literal=fingerprint=$OCI_FINGERPRINT \
  --from-literal=key="$OCI_KEY" \
  --from-literal=region=us-ashburn-1

# Apply CR with both providers
cat <<EOF | kubectl apply -f -
apiVersion: llmsafespaces.dev/v1
kind: InferenceRelay
metadata:
  name: relay-fleet
spec:
  providers:
    - provider: aws
      region: us-east-1
      credentialsRef:
        name: aws-relay-irwa
    - provider: oci
      region: us-ashburn-1
      credentialsRef:
        name: oci-credentials
EOF

# Wait for both to provision
kubectl get inferencerelay relay-fleet -o jsonpath='{.status.instances[*].provider}'
# Must show: aws oci

# Verify router prefers AWS
kubectl -n llmsafespaces port-forward deploy/relay-router 8080:8080 &
curl -s http://localhost:8080/metrics | grep relay_router_relay_healthy
# Both healthy, but traffic goes to AWS (weight 1000 vs OCI weight 100)

# Destroy the AWS relay — traffic should fall through to OCI
aws ec2 terminate-instances --instance-ids <aws-instance-id>
sleep 60
curl -s http://localhost:8080/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer public' \
  -d '{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'
# Must still succeed (OCI takes over)
```

**Pass criteria:** both providers provision; AWS gets traffic; when AWS fails, OCI takes over. (OCI driver is unvalidated code — this test may surface OCI-specific bugs.)

---

## Cleanup (after all tests)

```bash
# Delete the InferenceRelay CR (destroys all VMs)
kubectl delete inferencerelay --all

# Uninstall the chart
helm uninstall llmsafespaces -n llmsafespaces

# Delete the kind cluster
kind delete cluster --name relay-test

# Verify no orphan EC2 instances
aws ec2 describe-instances \
  --filters "Name=tag:managed-by,Values=llmsafespaces-relay" \
            "Name=instance-state-name,Values=running,pending" \
  --query 'Reservations[].Instances[].InstanceId' --output text
# Must be empty

# Orphan SG is acceptable (idempotent, free, reused on next deploy)
```

---

## Priority

| Test | Priority | Why |
|---|---|---|
| Test 1 (Helm deploys) | **P0** | If the chart doesn't render, nothing else works |
| Test 2 (Controller provisions) | **P0** | Validates the full reconcile loop + real EC2 from K8s |
| Test 3 (Router forwards) | **P0** | Validates the router→relay path live |
| Test 4 (Workspace traffic) | **P0** | The actual user-facing path — the whole point |
| Test 5 (Health checking) | **P1** | Failure detection — important but not blocking first deploy |
| Test 6 (Re-provisioning) | **P1** | Self-healing — important for production but not first validation |
| Test 7 (429 rotation) | **P2** | Hard to force deterministically; Zen's 429 behavior is external |
| Test 8 (CR deletion cleanup) | **P1** | Prevents orphaned VMs / cloud charges |
| Test 9 (Multi-provider) | **P2** | OCI driver is unvalidated code; AWS-only is sufficient for first deploy |

Tests 1-4 are the minimum viable validation. If those pass, the relay fleet is functionally complete and production-ready for a single-provider (AWS) deploy.
